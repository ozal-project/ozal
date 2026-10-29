#!/usr/bin/env python3
"""Verify TUN-002 CLI persistence/reconnect/revocation on disposable processes."""
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

    def run(*arguments):
        return subprocess.run([binary, *arguments], check=True, capture_output=True, text=True).stdout

    def stop(process):
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)
                raise AssertionError("process did not terminate gracefully")
            assert process.returncode == 0, "process did not shut down cleanly"

    def eventually(check, timeout=15):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            result = check()
            if result:
                return result
            time.sleep(0.05)
        raise AssertionError("persistent tunnel smoke timed out")

    with tempfile.TemporaryDirectory(prefix="ozal-persistent-smoke-") as scratch:
        directory = Path(scratch)
        pki = directory / "pki"
        db = str(directory / "state.db")
        run("tunnel-spike", "init", "-pki", str(pki))
        run("migrate", "-db", db)
        run("connector", "enroll", "-db", db, "-name", "portfolio", "-cert", str(pki / "connector.crt"))

        def identity(role):
            return ["-cert", str(pki / (role + ".crt")), "-key", str(pki / (role + ".key")), "-ca", str(pki / "ca.crt"), "-host", "portfolio.localhost"]

        log_path = directory / "processes.log"
        with log_path.open("w") as log:
            def start(*arguments):
                process = subprocess.Popen([binary, *arguments], stdout=log, stderr=log)
                processes.append(process)
                return process

            try:
                edge_args = ["tunnel", "edge", *identity("edge"), "-db", db, "-connector", "portfolio"]
                edge = start(*edge_args)

                def addresses():
                    assert edge.poll() is None, "edge exited before serving"
                    text = log_path.read_text()
                    http = re.search(r"HTTP http://(127\.0\.0\.1:\d+)", text)
                    tunnel = re.search(r"TUNNEL (127\.0\.0\.1:\d+)", text)
                    return (http[1], tunnel[1]) if http and tunnel else None

                http, ingress = eventually(addresses)

                def request(path="/", host="portfolio.localhost"):
                    req = urllib.request.Request("http://" + http + path, headers={"Host": host})
                    try:
                        with opener.open(req, timeout=3) as response:
                            return response.status, response.read()
                    except urllib.error.HTTPError as error:
                        return error.code, error.read()
                    except urllib.error.URLError:
                        return 0, b""

                assert request()[0] == 502, "offline connector did not fail closed"
                connector = start("tunnel", "connect", *identity("connector"), "-edge", ingress, "-server-name", "edge.localhost", "-root", str(root))
                eventually(lambda: request()[0] == 200)

                def check_site():
                    for path, file in [("/", "index.html"), ("/es/", "es/index.html")]:
                        code, body = request(path)
                        assert code == 200 and body == (root / file).read_bytes()
                    _, body = request()
                    assets = set(re.findall(rb'(?:src|href)="(/_astro/[^"?#]+)', body))
                    assert assets, "portfolio page has no hashed assets"
                    for asset in assets:
                        code, data = request(asset.decode())
                        assert code == 200 and data == (root / asset.decode().lstrip("/")).read_bytes()
                    assert request(host="unknown.localhost")[0] == 404
                    assert request("/.env")[0] == 404

                check_site()
                # Restart only the scratch edge; connector process and registry
                # stay intact and must recover automatically on the same port.
                connector_pid = connector.pid
                stop(edge)
                edge = start(*edge_args, "-listen", http, "-tunnel-listen", ingress)
                eventually(lambda: request()[0] == 200)
                assert connector.pid == connector_pid and connector.poll() is None
                check_site()
                assert "portfolio" in run("connector", "list", "-db", db)

                # Also prove recovery after an abrupt scratch-edge loss.
                edge.kill()
                edge.wait(timeout=3)
                edge = start(*edge_args, "-listen", http, "-tunnel-listen", ingress)
                eventually(lambda: request()[0] == 200)
                assert connector.poll() is None

                run("connector", "revoke", "-db", db, "-name", "portfolio")
                eventually(lambda: request()[0] == 502)
                # Wait for the reconnecting connector to be rejected by the DB.
                eventually(lambda: '"code":"connector_unauthorized"' in log_path.read_text())
                assert request()[0] == 502 and connector.poll() is None
                assert "revoked" in run("connector", "list", "-db", db)
                assert "up to date" in run("migrate", "-db", db)
                stop(connector)
                stop(edge)
                records = [json.loads(line) for line in log_path.read_text().splitlines() if line.startswith("{")]
                assert any(record.get("code") == "connector_revoked" for record in records)
                assert sum(record.get("msg") == "tunnel.connected" for record in records) >= 3
                print("PASS: persistent registry/identity, Portfolio ES/EN + assets, host/path restrictions,")
                print("      graceful and abrupt edge restart with automatic reattach, live revocation,")
                print("      reconnect rejected, idempotent migrations and clean shutdown.")
                print("      Only disposable databases, certificates and processes were used.")
            finally:
                for process in reversed(processes):
                    stop(process)


if __name__ == "__main__":
    main()
