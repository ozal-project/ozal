#!/usr/bin/env python3
"""Exercise separate edge/connector processes using disposable local identities."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--site-root", required=True)
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    root = Path(args.site_root).resolve()
    if not (root / "index.html").is_file() or not (root / "es/index.html").is_file():
        parser.error("site root must contain index.html and es/index.html")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    processes = []

    def stop(process):
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)
                raise AssertionError("process did not terminate gracefully")

    def eventually(check, timeout=10):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            result = check()
            if result:
                return result
            time.sleep(0.05)
        raise AssertionError("local smoke condition timed out")

    with tempfile.TemporaryDirectory(prefix="ozal-tunnel-smoke-") as scratch:
        directory = Path(scratch)
        pki = directory / "pki"
        subprocess.run([binary, "tunnel-spike", "init", "-pki", str(pki)], check=True, capture_output=True)
        edge_log = directory / "edge.log"
        with edge_log.open("w") as log:
            def start(arguments):
                process = subprocess.Popen([binary, "tunnel-spike", *arguments], stdout=log, stderr=log)
                processes.append(process)
                return process

            try:
                edge = start(["edge", "-pki", str(pki), "-listen", "127.0.0.1:0"])
                def addresses():
                    assert edge.poll() is None, "edge exited before serving"
                    text = edge_log.read_text()
                    http = re.search(r"HTTP http://(127\.0\.0\.1:\d+)", text)
                    tunnel = re.search(r"TUNNEL (127\.0\.0\.1:\d+)", text)
                    return (http[1], tunnel[1]) if http and tunnel else None
                http, tunnel = eventually(addresses)

                def request(path="/", host="portfolio.localhost"):
                    req = urllib.request.Request("http://" + http + path, headers={"Host": host})
                    try:
                        with opener.open(req, timeout=3) as response:
                            return response.status, response.read()
                    except urllib.error.HTTPError as error:
                        return error.code, error.read()
                assert request()[0] == 502, "offline connector must fail closed"
                connector_args = ["connect", "-pki", str(pki), "-edge", tunnel, "-root", str(root)]
                connector = start(connector_args)
                eventually(lambda: request()[0] == 200)
                for path in ["/", "/es/"]:
                    code, body = request(path)
                    assert code == 200 and body == (root / (path.strip("/") + "/index.html" if path != "/" else "index.html")).read_bytes()
                _, body = request()
                assets = set(re.findall(rb'(?:src|href)="(/_astro/[^"?#]+)', body))
                assert assets, "portfolio page has no hashed assets"
                for asset in assets:
                    code, data = request(asset.decode())
                    assert code == 200 and data == (root / asset.decode().lstrip("/")).read_bytes()
                assert request("/", "unknown.localhost")[0] == 404
                assert request("/.env")[0] == 404

                duplicate = start(connector_args)
                assert duplicate.wait(timeout=8) != 0, "duplicate connector accepted"
                assert connector.poll() is None and request()[0] == 200

                other_pki = directory / "untrusted-pki"
                subprocess.run([binary, "tunnel-spike", "init", "-pki", str(other_pki)], check=True, capture_output=True)
                intruder = start(["connect", "-pki", str(other_pki), "-edge", tunnel, "-root", str(root)])
                assert intruder.wait(timeout=8) != 0, "foreign CA accepted"
                assert request()[0] == 200

                connector.kill()  # simulate sudden connector loss, not a host service
                connector.wait(timeout=3)
                eventually(lambda: request()[0] == 502)
                connector = start(connector_args)
                eventually(lambda: request()[0] == 200)
                stop(connector)
                eventually(lambda: request()[0] == 502)
                stop(edge)
                records = []
                for line in edge_log.read_text().splitlines():
                    if line.startswith("{"):
                        records.append(json.loads(line))
                assert sum(record.get("msg") == "tunnel.connected" for record in records) == 2
                assert any(record.get("code") == "connector_busy" for record in records)
                assert any(record.get("code") == "tls_denied" for record in records)
                print("PASS: separate processes, ES/EN + assets, offline 502, unknown/hidden 404,")
                print("      duplicate/foreign CA rejected, abrupt disconnect + manual reconnect,")
                print("      graceful shutdown and lifecycle events. Disposable processes removed.")
            finally:
                for process in reversed(processes):
                    stop(process)


if __name__ == "__main__":
    main()
