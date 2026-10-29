#!/usr/bin/env python3
"""Separate-process TUN-002 acceptance; only disposable identities and loopback."""
import argparse
import http.server
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request


def wait_until(check, timeout=10):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        result = check()
        if result:
            return result
        time.sleep(0.05)
    raise RuntimeError("local smoke condition did not become ready")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--ready-file", type=Path)
    parser.add_argument("--release-file", type=Path)
    args = parser.parse_args()
    if bool(args.ready_file) != bool(args.release_file):
        parser.error("--ready-file and --release-file must be supplied together")
    binary = str(args.binary.resolve())
    processes = []
    logs = []

    class Origin(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            body = json.dumps({"source": "disposable-origin", "ip": self.headers.get("X-Forwarded-For"),
                               "identity": self.headers.get("X-Auth-Request-Email"),
                               "internal": self.headers.get("X-Ozal-Visitor-IP"),
                               "cookie": self.headers.get("Cookie")}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *_):
            pass

    origin = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Origin)
    threading.Thread(target=origin.serve_forever, daemon=True).start()
    try:
        with tempfile.TemporaryDirectory(prefix="ozal-configured-") as directory:
            root = Path(directory)
            pki, other = root / "pki", root / "other-pki"
            db = root / "state.db"

            def command(*arguments):
                subprocess.run([binary, *map(str, arguments)], check=True, stdout=subprocess.DEVNULL,
                               stderr=subprocess.PIPE, timeout=15)

            def start(name, *arguments):
                output = root / (name + ".log")
                log = output.open("wb")
                logs.append(log)
                process = subprocess.Popen([binary, *map(str, arguments)], stdout=log, stderr=log)
                processes.append(process)
                return process, output

            def stop(process):
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)
                        raise RuntimeError("local process failed graceful shutdown")

            for directory in (pki, other):
                command("tunnel-spike", "init", "-pki", directory)
            command("migrate", "-db", db)
            for name, directory in (("portfolio", pki), ("offline", other)):
                command("connector", "enroll", "-db", db, "-name", name, "-cert", directory / "connector.crt")
            with socket.socket() as reserved:
                reserved.bind(("127.0.0.1", 0))
                admin_bind = "127.0.0.1:" + str(reserved.getsockname()[1])
            token_file = root / "admin.token"
            token_file.write_text("smoke-admin-token\n")
            token_file.chmod(0o600)
            config = {"listen": "127.0.0.1:0", "admin": {"listen": admin_bind, "token": "file:" + str(token_file)},
                      "trusted_proxies": ["127.0.0.1/32"],
                      "tunnel": {"listen": "127.0.0.1:0", "cert_file": str(pki / "edge.crt"),
                                 "key_file": str(pki / "edge.key"), "ca_file": str(pki / "ca.crt")},
                      "routes": [{"host": "app.localhost", "connector": "portfolio"}]}
            config_path = root / "routing.json"

            def write_config():
                temporary = root / "routing.new"
                temporary.write_text(json.dumps(config))
                temporary.chmod(0o600)
                temporary.replace(config_path)

            def address(output, prefix):
                if edge.poll() is not None:
                    raise RuntimeError("disposable edge exited: " + output.read_text())
                for line in output.read_text().splitlines():
                    try:
                        event = json.loads(line)
                    except json.JSONDecodeError:
                        continue  # A concurrent log write may leave a partial last line.
                    message = event.get("msg", "")
                    if message.startswith(prefix):
                        return message[len(prefix):].split()[0]
                return None

            write_config()
            edge, output = start("edge", "-config", config_path, "-db", db)
            transport = wait_until(lambda: address(output, "tunnel listener: "))
            public = wait_until(lambda: address(output, "ozal listening on "))
            admin = wait_until(lambda: address(output, "ozal admin listening on "))
            connector, _ = start("connector", "tunnel", "connect", "-cert", pki / "connector.crt",
                                 "-key", pki / "connector.key", "-ca", pki / "ca.crt", "-host", "app.localhost",
                                 "-edge", transport, "-server-name", "edge.localhost",
                                 "-upstream", "http://127.0.0.1:" + str(origin.server_port))

            def fetch(address, path="/", host="app.localhost", extra=None):
                request = urllib.request.Request("http://" + address + path, headers={**(extra or {}), "Host": host,
                    "CF-Connecting-IP": "203.0.113.47", "X-Ozal-Visitor-IP": "6.6.6.6",
                    "X-Auth-Request-Email": "forged@example.test", "Cookie": "sb_session=forged; harmless=kept"})
                try:
                    with urllib.request.urlopen(request, timeout=3) as response:
                        return response.status, response.read()
                except urllib.error.HTTPError as error:
                    return error.code, error.read()

            wait_until(lambda: fetch(public)[0] == 200)
            status, body = fetch(public)
            result = json.loads(body)
            assert status == 200 and result == {"source": "disposable-origin", "ip": "203.0.113.47",
                "identity": None, "internal": None, "cookie": "harmless=kept"}, result
            assert fetch(public, host="unknown.localhost")[0] == 404
            config["routes"][0]["connector"] = "offline"
            write_config()
            edge.send_signal(signal.SIGHUP)
            wait_until(lambda: fetch(public)[0] == 502)
            config["routes"][0]["connector"] = "portfolio"
            write_config()
            edge.send_signal(signal.SIGHUP)
            wait_until(lambda: fetch(public)[0] == 200)
            config["routes"][0]["connector"] = "missing"
            write_config()
            edge.send_signal(signal.SIGHUP)
            wait_until(lambda: "not actively enrolled" in output.read_text())
            assert fetch(public)[0] == 200
            config["routes"][0]["connector"] = "portfolio"
            config["tunnel"]["listen"] = transport
            write_config()
            connector_pid = connector.pid
            stop(edge)
            edge, output = start("restarted-edge", "-config", config_path, "-db", db)
            public = wait_until(lambda: address(output, "ozal listening on "))
            admin = wait_until(lambda: address(output, "ozal admin listening on "))
            wait_until(lambda: fetch(public)[0] == 200)
            assert connector.poll() is None and connector.pid == connector_pid
            # Route state needs the admin credential even on loopback.
            status, _ = fetch(admin, "/_/overview", "127.0.0.1")
            assert status == 401
            status, body = fetch(admin, "/_/overview", "127.0.0.1", {"Authorization": "Bearer smoke-admin-token"})
            assert status == 200 and json.loads(body)["routes"][0]["connection"] == "Connector connected"
            status, body = fetch(admin, "/_/status", "127.0.0.1", {"Authorization": "Bearer smoke-admin-token"})
            assert status == 200 and b"Connector connected" in body
            if args.ready_file:
                args.ready_file.write_text(json.dumps({"admin": "http://" + admin, "public": "http://" + public}))
                wait_until(args.release_file.exists, timeout=180)
            command("connector", "revoke", "-db", db, "-name", "portfolio")
            wait_until(lambda: fetch(public)[0] == 502)
            wait_until(lambda: "connector_revoked" in output.read_text())
            stop(connector)
            stop(edge)
            assert edge.returncode == 0 and connector.returncode == 0
            print("OK: configured main process, trusted IP, spoof stripping, offline 502, atomic reload/rejection, console state, restart/reconnect and live revocation")
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
        for log in logs:
            log.close()
        origin.shutdown()
        origin.server_close()


if __name__ == "__main__":
    main()
