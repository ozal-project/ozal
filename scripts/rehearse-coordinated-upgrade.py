#!/usr/bin/env python3
"""REL-013/017: old/new edge+connector, mixed-version refusal and paired restore.

Development-only drill: accepts two already built, trusted binaries. Uses only
temporary state, synthetic credentials and loopback; never contacts a server.
"""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--previous", required=True, type=Path)
    parser.add_argument("--candidate", required=True, type=Path)
    args = parser.parse_args()
    previous, candidate = (str(p.resolve(strict=True)) for p in (args.previous, args.candidate))
    sums = [hashlib.sha256(Path(p).read_bytes()).hexdigest() for p in (previous, candidate)]
    if sums[0] == sums[1]:
        parser.error("previous and candidate must be distinct builds across the protocol change")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    processes, logs = [], []

    def run(binary, *arguments):
        # Captured output may contain fixture-only setup links: never print it.
        result = subprocess.run([binary, *map(str, arguments)], capture_output=True, timeout=30)
        if result.returncode:
            raise RuntimeError("fixture command failed: " + str(arguments[0]))
        return result.stdout

    def eventually(check, timeout=20):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if check():
                return
            time.sleep(0.1)
        raise RuntimeError("fixture did not reach the expected state")

    def stop(process):
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
                raise RuntimeError("fixture did not stop gracefully")
            if process.returncode != 0:
                raise RuntimeError("fixture shutdown failed")

    class Origin(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            body = json.dumps({"source": "rehearsal", "identity": self.headers.get("X-Auth-Request-Email"),
                               "cookie": self.headers.get("Cookie")}).encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *_):
            pass

    origin = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Origin)
    threading.Thread(target=origin.serve_forever, daemon=True).start()
    try:
        with tempfile.TemporaryDirectory(prefix="ozal-upgrade-drill-") as scratch:
            root = Path(scratch)
            root.chmod(0o700)
            db, pki = root / "state.db", root / "pki"
            # Reserve all ports together to ensure they are distinct.
            reservations = [socket.socket() for _ in range(3)]
            try:
                for s in reservations:
                    s.bind(("127.0.0.1", 0))
                public, admin, transport = ["127.0.0.1:" + str(s.getsockname()[1]) for s in reservations]
            finally:
                for s in reservations:
                    s.close()
            token = root / "fixture.token"
            token.write_text("rehearsal-only-admin\n")
            token.chmod(0o600)
            config_path, runtime = root / "routing.json", root / "runtime.json"
            config = {
                "listen": public, "admin": {"listen": admin, "token": "file:" + str(token)},
                "runtime_file": str(runtime),
                "auth": {"cookie_domain": "drill.localhost", "passkeys": {
                    "rp_id": "drill.localhost", "login_url": "http://login.drill.localhost:" + public.split(":")[1]}},
                "pages": {"dir": str(root / "pages"), "keep": 3},
                "tunnel": {"listen": transport, "cert_file": str(pki / "edge.crt"),
                           "key_file": str(pki / "edge.key"), "ca_file": str(pki / "ca.crt")},
                "routes": [{"host": "app.drill.localhost", "connector": "home"},
                           {"host": "private.drill.localhost", "connector": "home",
                            "access": {"allowed_emails": ["allowed@example.test"]}},
                           {"host": "site.drill.localhost", "site": "drill"}],
            }
            config_path.write_text(json.dumps(config))
            config_path.chmod(0o600)
            run(previous, "tunnel-spike", "init", "-pki", pki)
            run(previous, "migrate", "-db", db)
            run(previous, "connector", "enroll", "-db", db, "-name", "home", "-cert", pki / "connector.crt")
            run(previous, "runtime", "init", "-config", config_path)

            def start(binary, name, *arguments):
                log = (root / (name + ".log")).open("wb")
                logs.append(log)
                process = subprocess.Popen([binary, *map(str, arguments)], stdout=log, stderr=log)
                processes.append(process)
                return process

            def edge(binary, name):
                run(binary, "validate", "-config", config_path)
                process = start(binary, name, "-config", config_path, "-db", db)
                eventually(lambda: process.poll() is None and fetch(admin, "/_/healthz", "localhost")[0] == 200)
                return process

            def connector(binary, name):
                return start(binary, name, "tunnel", "connect", "-cert", pki / "connector.crt",
                             "-key", pki / "connector.key", "-ca", pki / "ca.crt", "-edge", transport,
                             "-server-name", "edge.localhost", "-host", "app.drill.localhost",
                             "-upstream", "http://127.0.0.1:" + str(origin.server_port),
                             "-route", "private.drill.localhost=upstream:http://127.0.0.1:" + str(origin.server_port))

            def fetch(address=public, path="/", host="app.drill.localhost"):
                request = urllib.request.Request("http://" + address + path, headers={"Host": host,
                    "X-Auth-Request-Email": "forged@example.test", "X-Ozal-Visitor-Email": "forged@example.test",
                    "X-Switchboard-Visitor-Email": "forged@example.test", "Cookie": "sb_session=forged; harmless=kept"})
                try:
                    with opener.open(request, timeout=2) as response:
                        return response.status, response.read(), response.headers
                except urllib.error.HTTPError as error:
                    return error.code, error.read(), error.headers
                except (urllib.error.URLError, TimeoutError):
                    return 0, b"", {}

            def check_pair():
                eventually(lambda: fetch()[0] == 200)
                assert json.loads(fetch()[1]) == {"source": "rehearsal", "identity": None, "cookie": "harmless=kept"}
                assert fetch(host="unknown.drill.localhost")[0] == 404
                status, _, headers = fetch(host="private.drill.localhost")
                assert status == 302 and "login.drill.localhost" in headers.get("Location", "")
                login_status = fetch(path="/_/auth/login", host="login.drill.localhost:" + public.split(":")[1])[0]
                assert login_status == 200, f"login page returned {login_status}"
                assert fetch(host="site.drill.localhost")[1] == b"before-upgrade"

            old_edge = edge(previous, "previous-edge")
            old_connector = connector(previous, "previous-connector")
            eventually(lambda: fetch()[0] == 200)
            site = root / "dist"
            site.mkdir()
            (site / "index.html").write_text("before-upgrade")
            run(previous, "site", "deploy", "-admin", "http://" + admin, "-token", "file:" + str(token), "-site", "drill", site)
            check_pair()
            print("PASS previous pair: tunnel, identity stripping, protected redirect, login page, publication", flush=True)

            # Quiesced checkpoint: no live files copied, no CA/key reads or copies.
            stop(old_connector)
            stop(old_edge)
            backup = root / "before.db"
            run(previous, "db", "backup", "-db", db, "-out", backup)
            runtime_before = runtime.read_bytes()
            # Preflight restore to a NEW path, never overwrite a live DB.
            run(previous, "db", "restore", "-from", backup, "-to", root / "restore-check.db")
            shutil.copytree(root / "pages", root / "pages-before")
            run(candidate, "migrate", "up", "-db", db, "-backup", root / "migration.db")
            print("Migration checkpoint: " + ("created" if (root / "migration.db").exists() else "no pending migrations between supplied builds"), flush=True)

            new_edge = edge(candidate, "candidate-edge")
            mixed_connector = connector(previous, "mixed-previous-connector")
            # The retry loop stays alive; refusal must persist, not be a startup race.
            for _ in range(30):
                assert fetch()[0] == 502 and mixed_connector.poll() is None, "candidate edge accepted previous connector or connector exited"
                time.sleep(0.1)
            stop(mixed_connector)
            new_connector = connector(candidate, "candidate-connector")
            check_pair()
            print("PASS candidate pair; candidate edge refuses previous connector", flush=True)
            # Publish a second build: rollback must recover external Pages state too.
            (site / "index.html").write_text("after-upgrade")
            run(candidate, "site", "deploy", "-admin", "http://" + admin, "-token", "file:" + str(token), "-site", "drill", site)
            assert fetch(host="site.drill.localhost")[1] == b"after-upgrade"
            stop(new_connector)
            stop(new_edge)

            # Restore into a new directory; leave candidate state intact for diagnosis.
            started = time.monotonic()
            restored = root / "restored.db"
            run(previous, "db", "restore", "-from", backup, "-to", restored)
            db = restored
            runtime.write_bytes(runtime_before)
            shutil.copytree(root / "pages-before", root / "pages-restored")
            config["pages"]["dir"] = str(root / "pages-restored")
            config_path.write_text(json.dumps(config))
            restored_edge = edge(previous, "restored-edge")
            mixed_connector = connector(candidate, "mixed-candidate-connector")
            for _ in range(30):
                assert fetch()[0] == 502 and mixed_connector.poll() is None, "previous edge accepted candidate connector or connector exited"
                time.sleep(0.1)
            stop(mixed_connector)
            restored_connector = connector(previous, "restored-connector")
            check_pair()
            print(f"PASS paired rollback: DB/runtime/Pages, previous protocol, candidate connector refused ({time.monotonic() - started:.1f}s fixture recovery)", flush=True)
            stop(restored_connector)
            stop(restored_edge)
            run(candidate, "migrate", "up", "-db", db, "-backup", root / "retry-migration.db")
            retry_edge = edge(candidate, "retry-edge")
            retry_connector = connector(candidate, "retry-connector")
            check_pair()
            stop(retry_connector)
            stop(retry_edge)
            print("PASS re-upgrade after rollback; previous sha256=" + sums[0] + "; candidate sha256=" + sums[1], flush=True)
            print("Local HTTP fixture only: no systemd/SSH, real browser login, ACME, public DNS or live server acceptance.")
    finally:
        try:
            for process in reversed(processes):
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)
        finally:
            for log in logs:
                log.close()
            origin.shutdown()
            origin.server_close()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


if __name__ == "__main__":
    # Private permissions also apply to fixture SQLite and Pages state.
    os.umask(0o077)
    main()
