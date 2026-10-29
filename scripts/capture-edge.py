#!/usr/bin/env python3
"""REL-013/017 edge checkpoint and isolated restore test.

Root mode reads the VPS edge state, writes a new private checkpoint under
/var/backups/ozal, and proves a restore on disposable loopback listeners.
It never stops, starts or reloads an active unit and never contacts the
connector or an application upstream.

--readable-only runs as octavio and captures only the readable inventory
(binaries, units, non-secret config structure) for rehearsal.
"""
import argparse
import grp
import hashlib
import http.client
import json
import os
import pwd
import re
import secrets
import shutil
import socket
import stat
import subprocess
import sys
import tempfile
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

PREVIEW = Path("/usr/local/lib/switchboard-preview/switchboard")
TUNNEL = Path("/usr/local/lib/switchboard-tunnel-edge/switchboard")
STAGING = Path("/home/octavio/.local/share/switchboard-staging/switchboard")
PREVIEW_DIR = Path("/usr/local/lib/switchboard-preview")
TUNNEL_DIR = Path("/usr/local/lib/switchboard-tunnel-edge")
PREVIEW_ETC = Path("/etc/switchboard-preview")
TUNNEL_ETC = Path("/etc/switchboard-tunnel-edge")
PREVIEW_STATE = Path("/var/lib/switchboard-preview")
TUNNEL_STATE = Path("/var/lib/switchboard-tunnel-edge")
PREVIEW_CONFIG = PREVIEW_ETC / "routing-preview.json"
TUNNEL_CONFIG = TUNNEL_ETC / "config.integrated.json"
UNITS = [
    ("switchboard-preview.service", "system"),
    ("switchboard-preview-staging.service", "system"),
    ("switchboard-tunnel-edge.service", "system"),
    ("switchboard-staging.service", "user"),
]
SECRET_NAME = re.compile(r"token|secret|password", re.I)

CURRENT = {
    "root": None,
    "index": None,
    "report": None,
    "args": None,
    "failures": [],
    "source_meta": {},
    "finalized": False,
    "finalizing": False,
}


def require(condition, label, detail=None):
    if not condition:
        abort(label, detail)


def abort(label, detail=None):
    state = CURRENT
    state["failures"].append({"check": label, "detail": detail})
    if state["root"] is not None and state["report"] is not None and not state["finalized"]:
        finalize(state["root"], state["index"], state["report"], state["args"], state["failures"], "incomplete")
    print(json.dumps({"status": "incomplete", "check": label, "detail": detail,
                      "private": str(state["root"])}, indent=2))
    raise SystemExit(1)


def now():
    return datetime.now(timezone.utc).isoformat()


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def owner_name(uid):
    try:
        return pwd.getpwuid(uid).pw_name
    except KeyError:
        return str(uid)


def group_name(gid):
    try:
        return grp.getgrgid(gid).gr_name
    except KeyError:
        return str(gid)


def run(cmd, *, user=None, timeout=120, check=True):
    if user:
        cmd = ["runuser", "-u", user, "--", *cmd]
    result = subprocess.run([str(c) for c in cmd], capture_output=True, timeout=timeout, text=True)
    if check and result.returncode:
        raise RuntimeError(f"command failed: {Path(str(cmd[0])).name} (exit {result.returncode})")
    return result


def file_metadata(path):
    info = path.lstat()
    entry = {"uid": info.st_uid, "gid": info.st_gid, "mode": stat.S_IMODE(info.st_mode)}
    if stat.S_ISLNK(info.st_mode):
        entry["type"] = "symlink"
        entry["target"] = os.readlink(path)
    elif stat.S_ISDIR(info.st_mode):
        entry["type"] = "dir"
    else:
        entry["type"] = "file"
    return entry


def tree_metadata(src):
    src = Path(src)
    items = {}

    def record(path):
        rel = "." if path == src else str(path.relative_to(src))
        items[rel] = file_metadata(path)

    record(src)
    for root, dirs, files in os.walk(src, followlinks=False):
        for name in dirs + files:
            record(Path(root) / name)
    return items


def apply_metadata(dst, items):
    # Deepest first: a directory mode is applied after its children.
    for rel in sorted(items, key=lambda value: value.count(os.sep), reverse=True):
        path = dst if rel == "." else dst / rel
        info = items[rel]
        if not path.exists() and not path.is_symlink():
            continue
        if info["type"] == "symlink":
            os.lchown(path, info["uid"], info["gid"])
        else:
            os.chown(path, info["uid"], info["gid"])
            os.chmod(path, info["mode"])


def verify_metadata(dst, items):
    mismatches = []
    for rel, info in items.items():
        path = dst if rel == "." else dst / rel
        if not path.exists() and not path.is_symlink():
            mismatches.append({"path": rel, "error": "missing"})
            continue
        actual = file_metadata(path)
        for field in ("uid", "gid", "mode", "type", "target"):
            if info.get(field) != actual.get(field):
                mismatches.append({"path": rel, "field": field, "source": info.get(field), "backup": actual.get(field)})
    return mismatches


def record_source(dst, items):
    root = CURRENT.get("root")
    if root is None:
        return
    for rel, info in items.items():
        path = dst if rel == "." else dst / rel
        CURRENT["source_meta"][str(path.relative_to(root))] = info


def copy_file(src, dst, *, record=True):
    dst.parent.mkdir(parents=True, exist_ok=True)
    source = Path(src)
    items = {".": file_metadata(source)}
    if items["."]["type"] == "symlink":
        if dst.is_symlink() or dst.exists():
            dst.unlink()
        os.symlink(items["."]["target"], dst)
    else:
        shutil.copy2(source, dst)
    if os.geteuid() == 0:
        apply_metadata(dst, items)
        mismatches = verify_metadata(dst, items)
        require(not mismatches, "copy_ownership", {"path": str(dst), "mismatches": mismatches})
    if record:
        record_source(dst, items)


def copy_tree(src, dst, *, ignore=None, record=True):
    if dst.exists():
        shutil.rmtree(dst)
    items = tree_metadata(Path(src))
    shutil.copytree(src, dst, symlinks=True, ignore=ignore)
    if os.geteuid() == 0:
        apply_metadata(dst, items)
        mismatches = verify_metadata(dst, items)
        require(not mismatches, "copy_ownership", {"path": str(dst), "mismatches": mismatches})
    if record:
        record_source(dst, items)


def ignore_staging(_directory, names):
    return [n for n in names if n.startswith(".staging-")]


def redact(value):
    if isinstance(value, dict):
        return {k: ("<redacted>" if SECRET_NAME.search(k) else redact(v)) for k, v in value.items()}
    if isinstance(value, list):
        return [redact(v) for v in value]
    return value


def secret_references(value):
    found = []
    if isinstance(value, dict):
        for item in value.values():
            found.extend(secret_references(item))
    elif isinstance(value, list):
        for item in value:
            found.extend(secret_references(item))
    elif isinstance(value, str):
        if value.startswith("file:"):
            found.append(("file", value[5:]))
        elif value.startswith("env:"):
            found.append(("env", value[4:]))
    return found


class UnixHTTPConnection(http.client.HTTPConnection):
    def __init__(self, path, timeout=5):
        super().__init__("localhost", timeout=timeout)
        self.unix_path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.unix_path)


def unix_http(sock, path, timeout=5):
    conn = UnixHTTPConnection(sock, timeout=timeout)
    try:
        conn.request("GET", path, headers={"Host": "localhost"})
        response = conn.getresponse()
        return response.status, response.read()
    finally:
        conn.close()


def wait_port(host, port, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with socket.create_connection((host, port), timeout=0.5):
                return True
        except OSError:
            time.sleep(0.1)
    return False


def sqlite_report(path):
    import sqlite3
    con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    try:
        integrity = con.execute("PRAGMA integrity_check").fetchone()[0]
        tables = [r[0] for r in con.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")]
        migrations = []
        if "schema_migrations" in tables:
            migrations = [r[0] for r in con.execute("SELECT version FROM schema_migrations ORDER BY version")]
        return {"integrity": integrity, "tables": tables, "migrations": migrations}
    finally:
        con.close()


def sqlite_rows(path, query):
    import sqlite3
    con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    try:
        return [dict(row) for row in con.execute(query)]
    finally:
        con.close()


def live_fingerprint():
    site_live = sqlite_rows(PREVIEW_STATE / "state.db",
                            "SELECT site, deployment_id, changed_at FROM site_live ORDER BY site")
    tls = []
    certs_root = PREVIEW_STATE / "origin-cert" / "certs"
    if certs_root.exists():
        for host in sorted(certs_root.iterdir()):
            if not host.is_dir():
                continue
            for generation in sorted(host.iterdir()):
                if generation.is_dir() and (generation / "complete").exists():
                    tls.append(host.name + "/" + generation.name)
    accounts = []
    account_root = PREVIEW_STATE / "origin-cert" / "account"
    if account_root.exists():
        for account in sorted(account_root.glob("*.json")):
            accounts.append({"file": account.name, "sha256": sha256(account)})
    try:
        registry = [
            {key: (value.decode(errors="replace") if isinstance(value, bytes) else value)
             for key, value in row.items()}
            for row in sqlite_rows(TUNNEL_STATE / "state.db",
                                   "SELECT name, fingerprint, revoked_at FROM connectors ORDER BY name")
        ]
    except Exception as exc:
        registry = [{"error": type(exc).__name__}]
    current = TUNNEL_STATE / "current"
    return {
        "preview_config_sha256": sha256(PREVIEW_CONFIG),
        "tunnel_config_sha256": sha256(TUNNEL_CONFIG),
        "runtime_sha256": sha256(PREVIEW_STATE / "runtime.json") if (PREVIEW_STATE / "runtime.json").exists() else None,
        "tunnel_current": os.readlink(current) if current.is_symlink() else None,
        "tls_generations": tls,
        "tls_accounts": accounts,
        "connector_registry": registry,
        "site_live": site_live,
    }


def db_backup(binary, db, out, user, report, role):
    # The service account must be able to traverse the staging directory.
    # /var/tmp is world-traversable; the staging directory itself is 0700 and
    # owned by the service. The private checkpoint is not on this path.
    stage = Path(tempfile.mkdtemp(prefix=f"ozal-rel013-017-{role}-", dir="/var/tmp"))
    try:
        info = pwd.getpwnam(user)
        os.chown(stage, info.pw_uid, info.pw_gid)
        os.chmod(stage, 0o700)
        target = stage / out.name
        result = run([binary, "db", "backup", "-db", db, "-out", target], user=user, timeout=180, check=False)
        failed = out.parent.parent / "failed-staging" / role
        failure = None
        if result.returncode or not target.exists():
            failure = {"db": str(db), "method": "binary db backup", "ok": False,
                       "stderr_class": (result.stderr or "").strip().splitlines()[:1]}
        else:
            verify = sqlite_report(target)
            if verify["integrity"] != "ok":
                failure = {"db": str(db), "method": "binary db backup", "ok": False,
                           "integrity": verify["integrity"]}
        if failure is not None:
            failed.mkdir(parents=True, exist_ok=True)
            shutil.copytree(stage, failed / ("stage-" + now().replace(":", "")), symlinks=True)
            report.append(failure)
            abort("db_backup", failure)
        out.parent.mkdir(parents=True, exist_ok=True)
        verify = sqlite_report(target)
        shutil.move(target, out)
        os.chown(out, 0, 0)
        os.chmod(out, 0o600)
        report.append({"db": str(db), "method": "binary db backup (VACUUM INTO, integrity check)",
                       "ok": True, "sha256": sha256(out), "size": out.stat().st_size,
                       "integrity": verify["integrity"], "migrations": verify["migrations"]})
        return out
    finally:
        shutil.rmtree(stage, ignore_errors=True)


def cert_meta(path):
    result = run(["openssl", "x509", "-in", path, "-noout", "-subject", "-issuer", "-serial", "-dates",
                  "-fingerprint", "-sha256"], check=False)
    if result.returncode:
        return {"error": "unreadable certificate"}
    return {"lines": result.stdout.strip().splitlines()}


def key_cert_match(cert, key):
    cert_pub = run(["openssl", "x509", "-in", cert, "-pubkey", "-noout"], check=False)
    key_pub = run(["openssl", "pkey", "-in", key, "-pubout"], check=False)
    if cert_pub.returncode or key_pub.returncode:
        return False
    return cert_pub.stdout == key_pub.stdout


def cert_chain(ca, cert):
    result = run(["openssl", "verify", "-CAfile", str(ca), str(cert)], check=False)
    return result.returncode == 0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--readable-only", action="store_true")
    parser.add_argument("--quiesced", action="store_true",
                        help="final checkpoint: the preview and tunnel-edge units are stopped")
    parser.add_argument("--allow-external-secrets", action="store_true",
                        help="acknowledge env: secrets that cannot be captured from disk")
    parser.add_argument("--expect-connector-fingerprint", default="")
    args = parser.parse_args()
    os.umask(0o077)
    if args.readable_only and args.quiesced:
        parser.error("--quiesced cannot be combined with --readable-only")
    if not args.readable_only and os.geteuid() != 0:
        parser.error("root is required; use --readable-only for the octavio inventory")
    if not args.readable_only and not args.expect_connector_fingerprint:
        parser.error("--expect-connector-fingerprint is required for a root capture")
    reader = pwd.getpwnam("octavio") if args.readable_only else None

    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    if args.readable_only:
        root = Path.home() / ".local/state/ozal-backups" / f"REL-013-017-{stamp}-readable"
        private = None
    else:
        root = Path("/var/backups/ozal") / f"REL-013-017-{stamp}"
        private = root
    index = Path("/home/octavio/.local/state/ozal-backups") / f"REL-013-017-{stamp}-index"
    root.mkdir(parents=True, exist_ok=False)
    os.chmod(root, 0o700)
    index.mkdir(parents=True, exist_ok=False)
    os.chmod(index, 0o700)
    octavio = pwd.getpwnam("octavio")
    if not args.readable_only:
        os.chown(index.parent, octavio.pw_uid, octavio.pw_gid)
        os.chown(index, octavio.pw_uid, octavio.pw_gid)

    def own_index(path):
        if not args.readable_only:
            os.chown(path, octavio.pw_uid, octavio.pw_gid)

    report = {
        "format": "ozal-rel013-017-edge-backup/1",
        "created_utc": now(),
        "mode": "readable-only" if args.readable_only else "root",
        "host": os.uname().nodename,
        "private_root": str(root),
        "index_root": str(index),
        "capture": {
            "method": ("quiesced final checkpoint" if args.quiesced else
                       "read-only copies and online SQLite snapshots while units run")
                      + "; no unit stop/start/reload by this tool; no timer change",
            "sqlite": "installed binary db backup (VACUUM INTO + integrity check); restore verified on a new path",
            "restore": "disposable loopback listeners with a restored DB/Pages copy; no production TLS or tunnel identity served",
        },
        "limitations": [
            "This checkpoint is on the edge host only. An off-host copy is a separate authorized operation.",
            "No durable audit is configured on either edge config; audit.verify is retained but has no chain here.",
            "Restore tests use the previous binary and copies; they do not prove a real connector/login/DNS/ACME acceptance.",
            "The connector's registry authorization is checked from the edge DB fingerprint only.",
        ],
        "status": "running",
        "failures": [],
        "coherence": {},
        "units": [],
        "binaries": [],
        "configs": [],
        "secrets": [],
        "external_secrets": [],
        "db": [],
        "pages": {},
        "runtime": {},
        "tls": {},
        "tunnel_identity": {},
        "registry": {},
        "audit": {},
        "status": {},
        "restore": {},
        "checks": [],
    }
    CURRENT.update(root=root, index=index, report=report, args=args, failures=[], source_meta={}, finalized=False)

    def note(name, **values):
        report["checks"].append({"name": name, "time": now(), **values})

    # Units and environment
    (root / "systemd").mkdir(exist_ok=True)
    run(["date", "-u"], check=False)
    processes = run(["ps", "-eo", "pid,user,args"], check=False).stdout
    processes = "\n".join(line for line in processes.splitlines() if not SECRET_NAME.search(line))
    (root / "systemd/environment.txt").write_text(
        "\n".join([
            "date_utc=" + now(),
            "uname=" + " ".join(os.uname()),
            "euid=%d" % os.geteuid(),
            "systemctl=" + run(["systemctl", "--version"], check=False).stdout.splitlines()[0],
            "listeners=" + run(["ss", "-ltnp"], check=False).stdout,
            "processes=" + processes,
        ]) + "\n")
    for unit, scope in UNITS:
        entry = {"unit": unit, "scope": scope}
        cmd = ["systemctl"] + (["--user"] if scope == "user" else []) + ["show", unit]
        if scope == "user":
            command = ["/usr/bin/env", "XDG_RUNTIME_DIR=/run/user/1000", *cmd]
            result = run(command, check=False) if args.readable_only else run(command, user="octavio", check=False)
        else:
            result = run(cmd, check=False)
        entry["show_exit"] = result.returncode
        (root / "systemd" / (unit + ".show")).write_text(result.stdout)
        cat = ["systemctl"] + (["--user"] if scope == "user" else []) + ["cat", unit]
        if scope == "user":
            command = ["/usr/bin/env", "XDG_RUNTIME_DIR=/run/user/1000", *cat]
            result = run(command, check=False) if args.readable_only else run(command, user="octavio", check=False)
        else:
            result = run(cat, check=False)
        (root / "systemd" / (unit + ".cat")).write_text(result.stdout + result.stderr)
        entry["cat_exit"] = result.returncode
        entry["active"] = run(["systemctl", "is-active", unit], check=False).stdout.strip() if scope == "system" else "see user show"
        entry["enabled"] = run(["systemctl", "is-enabled", unit], check=False).stdout.strip() if scope == "system" else "see user show"
        for fragment in re.findall(r"FragmentPath=(\S+)", result.stdout):
            entry["fragment"] = fragment
        report["units"].append(entry)

    if not args.readable_only:
        states = {}
        for unit in ("switchboard-preview.service", "switchboard-tunnel-edge.service"):
            state = run(["systemctl", "is-active", unit], check=False).stdout.strip()
            states[unit] = state
            if args.quiesced:
                require(state == "inactive", "unit_active_during_quiesced_capture", {"unit": unit, "state": state})
            else:
                require(state == "active", "unit_not_active", {"unit": unit, "state": state})
        report["unit_state"] = states
        report["coherence"]["before"] = live_fingerprint()

    # Binaries
    (root / "bin").mkdir(exist_ok=True)
    for source in sorted(PREVIEW_DIR.glob("switchboard*")) + [TUNNEL, STAGING]:
        if not source.is_file() or not os.access(source, os.X_OK):
            continue
        target = root / "bin" / source.name
        if target.exists():
            target = root / "bin" / (source.parent.name + "-" + source.name)
        copy_file(source, target)
        meta = {"source": str(source), "path": str(target.relative_to(root)), "sha256": sha256(target),
                "mode": oct(stat.S_IMODE(source.stat().st_mode)), "size": source.stat().st_size,
                "owner": owner_name(source.stat().st_uid)}
        if source == PREVIEW:
            version = run([PREVIEW, "version", "-json"], check=False)
            try:
                meta["build"] = json.loads(version.stdout)
            except Exception:
                meta["build"] = {"error": "version output not JSON"}
        report["binaries"].append(meta)
    upgrade_log = PREVIEW_DIR / "switchboard.upgrade.log"
    if not args.readable_only and upgrade_log.exists():
        copy_file(upgrade_log, root / "bin" / "switchboard.upgrade.log")
        report["binaries"].append({"source": str(upgrade_log), "path": "bin/switchboard.upgrade.log",
                                   "sha256": sha256(root / "bin/switchboard.upgrade.log"),
                                   "mode": oct(stat.S_IMODE(upgrade_log.stat().st_mode)),
                                   "size": upgrade_log.stat().st_size})

    # Configs and secret references
    (root / "config").mkdir(exist_ok=True)
    for pattern in ("routing-preview.json*", "routing-preview-staging.json", "origin-pull-ca.crt",
                    "staging-roots.pem", "*.token"):
        for source in sorted(PREVIEW_ETC.glob(pattern)):
            if not source.is_file():
                continue
            target = root / "config" / source.name
            try:
                copy_file(source, target)
            except PermissionError:
                report["configs"].append({"source": str(source), "unreadable": True,
                                          "mode": oct(stat.S_IMODE(source.stat().st_mode))})
                continue
            entry = {"source": str(source), "path": str(target.relative_to(root)), "sha256": sha256(target),
                     "mode": oct(stat.S_IMODE(source.stat().st_mode)), "owner": owner_name(source.stat().st_uid),
                     "group": group_name(source.stat().st_gid), "size": source.stat().st_size}
            report["configs"].append(entry)
            if source.name.endswith(".token"):
                report["secrets"].append({"path": entry["path"], "mode": entry["mode"], "sha256": entry["sha256"]})
    for source in sorted(TUNNEL_ETC.iterdir()):
        if source.is_file():
            target = root / "config" / ("tunnel-edge-" + source.name)
            copy_file(source, target)
            report["configs"].append({"source": str(source), "path": str(target.relative_to(root)),
                                      "sha256": sha256(target), "mode": oct(stat.S_IMODE(source.stat().st_mode)),
                                      "owner": owner_name(source.stat().st_uid)})
    for source in (Path("/etc/sudoers.d/switchboard-tunnel.sudoers"),):
        if source.exists():
            target = root / "config" / source.name
            copy_file(source, target)
            report["configs"].append({"source": str(source), "path": str(target.relative_to(root)),
                                      "sha256": sha256(target), "mode": oct(stat.S_IMODE(source.stat().st_mode)),
                                      "owner": owner_name(source.stat().st_uid)})

    if not args.readable_only:
        copied_sources = {entry.get("source"): entry.get("path") for entry in report["configs"]
                          if entry.get("source") and not entry.get("unreadable")}
        references = []
        for path in (PREVIEW_CONFIG, TUNNEL_CONFIG):
            try:
                references.extend(secret_references(json.loads(path.read_text())))
            except Exception as exc:
                abort("config_reference_scan", {"config": str(path), "error": type(exc).__name__})
        seen = set()
        for kind, reference in references:
            if (kind, reference) in seen:
                continue
            seen.add((kind, reference))
            if kind == "env":
                report["external_secrets"].append(reference)
                require(args.allow_external_secrets, "external_secret_not_captured", reference)
                continue
            source = Path(reference)
            require(source.is_file(), "secret_file_missing", reference)
            if reference in copied_sources:
                continue
            target = root / "config" / ("secret-" + hashlib.sha256(reference.encode()).hexdigest()[:12] + "-" + source.name)
            copy_file(source, target)
            entry = {"reference": reference, "path": str(target.relative_to(root)), "sha256": sha256(target),
                     "mode": oct(stat.S_IMODE(source.stat().st_mode)), "size": source.stat().st_size}
            report["configs"].append(entry)
            report["secrets"].append(entry)

    # Redacted config for the readable index
    redacted = {}
    for path in (PREVIEW_CONFIG, TUNNEL_CONFIG):
        try:
            redacted[path.name] = redact(json.loads(path.read_text()))
        except Exception:
            redacted[path.name] = {"error": "not readable as JSON"}
    (index / "config-redacted.json").write_text(json.dumps(redacted, indent=2, sort_keys=True) + "\n")
    os.chmod(index / "config-redacted.json", 0o600)

    # Status through the admin socket (read-only, no bearer)
    for label, sock in (("preview", "/run/switchboard-preview/admin.sock"),
                        ("tunnel-edge", "/run/switchboard-tunnel-edge/admin.sock")):
        entry = {"socket": sock}
        if args.quiesced:
            # The units are stopped by contract, so their admin sockets do not
            # exist; liveness is covered by the live preliminary capture and by
            # the restore probes below, not by the stopped services.
            entry["skipped"] = "quiesced: unit stopped, admin socket absent"
            report["status"][label] = entry
            continue
        try:
            status, body = unix_http(sock, "/_/healthz")
            entry["healthz"] = {"status": status, "body": body.decode(errors="replace")[:200]}
            status, body = unix_http(sock, "/_/api/status")
            entry["api_status_http"] = status
            try:
                entry["api_status"] = redact(json.loads(body))
            except Exception:
                entry["api_status"] = {"error": "not JSON", "bytes": len(body)}
        except Exception as exc:
            entry["error"] = type(exc).__name__
        report["status"][label] = entry
        if not args.readable_only:
            # The tunnel edge build installed on the VPS predates /_/healthz;
            # its status API is the authoritative liveness and config probe.
            if label == "preview":
                require(entry.get("healthz", {}).get("status") == 200, "status_healthz_failed", label)
            require(entry.get("api_status_http") == 200, "status_api_failed", label)
            require(bool(entry.get("api_status", {}).get("config_sha256")), "status_config_digest_missing", label)

    if args.readable_only:
        report["status"] = "readable-only"
        (index / "manifest-public.json").write_text(json.dumps(redact(report), indent=2, sort_keys=True) + "\n")
        os.chmod(index / "manifest-public.json", 0o600)
        print(json.dumps({"index": str(index), "mode": "readable-only",
                          "binaries": len(report["binaries"]), "configs": len(report["configs"]),
                          "pending": "private DB/pages/TLS/tunnel/audit capture requires root"}, indent=2))
        return

    # Online SQLite snapshots
    (root / "db").mkdir(exist_ok=True)
    preview_db = db_backup(PREVIEW, PREVIEW_STATE / "state.db", root / "db/preview-state.db", "switchboard",
                           report["db"], "preview")
    tunnel_db = db_backup(PREVIEW, TUNNEL_STATE / "state.db", root / "db/tunnel-state.db", "switchboard-tunnel",
                          report["db"], "tunnel")
    for label, db in (("preview", preview_db), ("tunnel", tunnel_db)):
        verify = sqlite_report(db)
        require(verify["integrity"] == "ok", "db_integrity", {"name": label, "verify": verify})
        report["db"].append({"name": label, "verify": verify})

    # Pages: snapshot first, then copy exactly what that snapshot references.
    live_site = None
    if preview_db:
        metadata = {
            "deployments": sqlite_rows(preview_db, "SELECT site, id, created_at, files, bytes FROM deployments ORDER BY site, created_at, id"),
            "site_live": sqlite_rows(preview_db, "SELECT site, deployment_id, changed_at FROM site_live ORDER BY site"),
        }
        pages_src = PREVIEW_STATE / "pages"
        require(pages_src.exists(), "pages_source_missing", str(pages_src))
        pages_dst = root / "pages"
        copy_tree(pages_src, pages_dst, ignore=ignore_staging)
        referenced = {(row["site"], row["id"]) for row in metadata["deployments"]}
        missing = []
        for site, deployment in sorted(referenced):
            if not (pages_dst / "sites" / site / deployment).is_dir():
                missing.append({"site": site, "deployment": deployment})
        files = []
        for site, deployment in sorted(referenced):
            directory = pages_dst / "sites" / site / deployment
            if directory.is_dir():
                for path in sorted(directory.rglob("*")):
                    if path.is_file():
                        files.append({"site": site, "deployment": deployment,
                                      "path": str(path.relative_to(pages_dst)), "size": path.stat().st_size,
                                      "sha256": sha256(path)})
        live = metadata["site_live"][0] if metadata["site_live"] else None
        require(live is not None, "pages_live_missing", "site_live has no row")
        require(not missing, "pages_consistency", {"missing": missing})
        live_dir = pages_dst / "sites" / live["site"] / live["deployment_id"]
        require((live_dir / "index.html").is_file(), "pages_live_index_missing", str(live_dir))
        live_site = live["site"]
        report["pages"] = {"source": str(pages_src), "copied": str(pages_dst), "metadata": metadata,
                           "referenced_deployments": len(referenced), "missing": missing,
                           "files": len(files), "bytes": sum(f["size"] for f in files),
                           "live": live, "file_manifest": files}
        report["checks"].append({"name": "pages_consistency", "ok": True, "deployments": len(referenced)})

    # Runtime
    runtime = PREVIEW_STATE / "runtime.json"
    require(runtime.exists(), "runtime_missing", str(runtime))
    first = sha256(runtime)
    copy_file(runtime, root / "runtime.json")
    second = sha256(runtime)
    require(first == second, "runtime_changed_during_capture", {"before": first, "after": second})
    try:
        parsed = json.loads((root / "runtime.json").read_text())
        require(isinstance(parsed, dict), "runtime_not_object", None)
        keys = sorted(parsed.keys())
    except Exception as exc:
        abort("runtime_unreadable", type(exc).__name__)
    report["runtime"] = {"source": str(runtime), "sha256": first, "stable": True, "keys": keys,
                         "mode": oct(stat.S_IMODE(runtime.stat().st_mode)),
                         "owner": owner_name(runtime.stat().st_uid)}

    # TLS/ACME state
    tls_src = PREVIEW_STATE / "origin-cert"
    tls_dst = root / "tls"
    certs = []
    require(tls_src.exists(), "tls_state_missing", str(tls_src))
    copy_tree(tls_src, tls_dst)
    cert_dirs = sorted((tls_dst / "certs").glob("*")) if (tls_dst / "certs").exists() else []
    for host_dir in cert_dirs:
        for generation in sorted(host_dir.glob("*")):
            cert, key, marker = generation / "cert.pem", generation / "key.pem", generation / "complete"
            if not marker.exists():
                continue
            entry = {"host": host_dir.name, "generation": generation.name,
                     "complete": True, "has_cert": cert.exists(), "has_key": key.exists()}
            require(cert.exists() and key.exists(), "tls_generation_incomplete", str(generation))
            entry["metadata"] = cert_meta(cert)
            entry["key_cert_match"] = key_cert_match(cert, key)
            require(entry["key_cert_match"], "tls_key_cert_mismatch", str(generation))
            certs.append(entry)
    require(bool(certs), "tls_generations_missing", str(tls_dst / "certs"))
    accounts = []
    account_files = sorted((tls_dst / "account").glob("*.json")) if (tls_dst / "account").exists() else []
    for account in account_files:
        try:
            data = json.loads(account.read_text())
            accounts.append({"file": account.name, "directory_url": data.get("DirectoryURL"),
                             "kid_present": bool(data.get("KID")), "key_present": bool(data.get("KeyPEM"))})
        except Exception:
            accounts.append({"file": account.name, "error": "unreadable"})
    intents = list((tls_dst / "dns-intents").glob("*.json")) if (tls_dst / "dns-intents").exists() else []
    report["tls"] = {"source": str(tls_src), "copied": str(tls_dst), "certs": certs,
                     "accounts": accounts, "dns_intents": len(intents),
                     "directory_marker": (tls_dst / "acme-directory").read_text().strip()
                     if (tls_dst / "acme-directory").exists() else None}

    # Tunnel identity files (not the live DB)
    tun_dst = root / "tunnel"
    tun_dst.mkdir(exist_ok=True)
    identity_meta = {}
    for name in ("identity", "identities"):
        source = TUNNEL_STATE / name
        require(source.exists(), "tunnel_identity_dir_missing", str(source))
        copy_tree(source, tun_dst / name)
    current = TUNNEL_STATE / "current"
    require(current.is_symlink(), "tunnel_current_missing", str(current))
    link = os.readlink(current)
    os.symlink(link, tun_dst / "current")
    current_info = file_metadata(current)
    os.lchown(tun_dst / "current", current_info["uid"], current_info["gid"])
    CURRENT["source_meta"][str((tun_dst / "current").relative_to(root))] = current_info
    identity_meta["current"] = link
    target = tun_dst / link
    require(target.is_dir(), "tunnel_current_target_missing", link)
    for name in ("edge.crt", "edge.key", "ca.crt"):
        path = target / name
        require(path.is_file(), "tunnel_identity_file_missing", str(path))
        identity_meta[name] = {"path": str(path.relative_to(root)), "sha256": sha256(path),
                               "mode": oct(stat.S_IMODE(path.stat().st_mode))}
    identity_meta["key_cert_match"] = key_cert_match(target / "edge.crt", target / "edge.key")
    require(identity_meta["key_cert_match"], "tunnel_key_cert_mismatch", str(target))
    identity_meta["metadata"] = cert_meta(target / "edge.crt")
    identity_meta["chain_verify"] = cert_chain(target / "ca.crt", target / "edge.crt")
    require(identity_meta["chain_verify"], "tunnel_chain_invalid", str(target))
    report["tunnel_identity"] = identity_meta

    # Connector registry from the tunnel snapshot
    tables = sqlite_report(tunnel_db)["tables"]
    require("connectors" in tables, "connector_registry_missing", str(tunnel_db))
    rows = sqlite_rows(tunnel_db, "SELECT * FROM connectors ORDER BY name")
    columns = sorted(rows[0].keys()) if rows else []
    expected = args.expect_connector_fingerprint.lower().replace(":", "")
    require(bool(expected), "connector_fingerprint_missing", "use --expect-connector-fingerprint")
    public_rows = []
    matches = False
    for row in rows:
        # Schema (migration 002): cert_sha256 is the 32-byte BLOB of the
        # enrolled leaf certificate. Compare its hex form only; never print it.
        digest = row.get("cert_sha256")
        hex_digest = digest.hex() if isinstance(digest, bytes) else ""
        active = row.get("revoked_at") is None
        if hex_digest == expected and active:
            matches = True
        public_rows.append({"name": row.get("name"), "created_at": row.get("created_at"),
                            "revoked_at": row.get("revoked_at"),
                            "cert_sha256_matches_expected": hex_digest == expected})
    report["registry"] = {"columns": columns, "rows": public_rows,
                          "expected_connector_fingerprint": expected, "expected_match": matches}
    require(matches, "connector_not_active_in_registry",
            {"expected": expected, "columns": columns, "rows": public_rows})

    # Audit: explicit absence or copied chain
    audit_dirs = [p for p in (PREVIEW_STATE / "audit", TUNNEL_STATE / "audit") if p.exists()]
    configs = []
    for path in (PREVIEW_CONFIG, TUNNEL_CONFIG):
        try:
            configs.append(json.loads(path.read_text()))
        except Exception:
            pass
    configured = [c.get("audit") for c in configs if isinstance(c, dict) and c.get("audit")]
    if audit_dirs:
        for source in audit_dirs:
            copy_tree(source, root / "audit" / source.parent.name)
        report["audit"] = {"configured": configured, "copied": [str(p) for p in audit_dirs]}
    else:
        report["audit"] = {"configured": configured, "copied": [], "note": "no audit directory found; configs have no audit section"}

    # Restore test: DB/Pages/runtime as the service account, loopback only.
    # The service accounts must traverse the test directory, so it lives in
    # a root-owned, traverse-only (0711) temporary root under /var/tmp, not
    # inside the private checkpoint. It is removed when the run ends.
    restore = Path(tempfile.mkdtemp(prefix="ozal-rel013-017-restore-", dir="/var/tmp"))
    os.chmod(restore, 0o711)
    CURRENT.setdefault("cleanup", []).append(str(restore))
    preview_restore = restore / "preview"
    preview_restore.mkdir(mode=0o700)
    preview_user = pwd.getpwnam("switchboard")
    os.chown(preview_restore, preview_user.pw_uid, preview_user.pw_gid)
    report["restore"]["preview"] = {}
    preview_seed = preview_restore / "preview-state.backup.db"
    shutil.copy2(preview_db, preview_seed)
    os.chown(preview_seed, preview_user.pw_uid, preview_user.pw_gid)
    os.chmod(preview_seed, 0o600)
    result = run([PREVIEW, "db", "restore", "-from", preview_seed, "-to", preview_restore / "state.db"],
                 user="switchboard", check=False)
    if result.returncode != 0:
        # Keep only the first line of diagnostics: paths and SQLite messages,
        # never database contents.
        first = (result.stderr or result.stdout or "").strip().splitlines()[:1]
        require(False, "restore_preview_db", {"exit": result.returncode, "message": first})
    report["restore"]["preview"]["restore_exit"] = result.returncode
    verify = sqlite_report(preview_restore / "state.db")
    require(verify["integrity"] == "ok", "restore_preview_integrity", verify)
    report["restore"]["preview"]["verify"] = verify
    copy_tree(root / "pages", preview_restore / "pages", record=False)
    copy_file(root / "runtime.json", preview_restore / "runtime.json", record=False)
    fixture_token = preview_restore / "admin.token"
    fixture_token.write_text(secrets.token_hex(32) + "\n")
    os.chown(fixture_token, preview_user.pw_uid, preview_user.pw_gid)
    os.chmod(fixture_token, 0o600)
    live_row = report["pages"]["live"]
    live_dir = preview_restore / "pages/sites" / live_row["site"] / live_row["deployment_id"]
    pages_index_html = live_dir / "index.html"
    require(pages_index_html.is_file(), "restore_pages_index_missing", str(pages_index_html))
    access = {}
    for label, path in (("db", preview_restore / "state.db"),
                        ("pages_index", pages_index_html),
                        ("runtime", preview_restore / "runtime.json")):
        check = run(["test", "-r", path], user="switchboard", check=False)
        access[label] = check.returncode == 0
        require(access[label], "restore_access_denied", {"as": "switchboard", "path": str(path)})
    report["restore"]["preview"]["service_access"] = access
    listen = "127.0.0.1:%d" % free_port()
    admin = "127.0.0.1:%d" % free_port()
    base = {
        "listen": listen,
        "admin": {"listen": admin, "socket": str(preview_restore / "admin.sock"), "token": "file:" + str(fixture_token)},
        "pages": {"dir": str(preview_restore / "pages"), "keep": 5},
        "routes": [{"host": "restore.test", "site": live_site}],
    }
    config_path = preview_restore / "restore-pages.json"
    config_path.write_text(json.dumps(base))
    os.chown(config_path, preview_user.pw_uid, preview_user.pw_gid)
    os.chmod(config_path, 0o600)
    validate = run([PREVIEW, "validate", "-config", config_path], user="switchboard", check=False)
    require(validate.returncode == 0, "restore_pages_config_validate", validate.returncode)
    report["restore"]["preview"]["pages_config_validate_exit"] = validate.returncode
    log = (preview_restore / "edge.log").open("wb")
    process = subprocess.Popen([str(PREVIEW), "-config", str(config_path), "-db", str(preview_restore / "state.db")],
                               stdout=log, stderr=log, user="switchboard", group="switchboard", extra_groups=[])
    try:
        require(wait_port("127.0.0.1", int(listen.rsplit(":", 1)[1])), "restore_listener_not_open", listen)
        request = urllib.request.Request("http://" + listen + "/", headers={"Host": "restore.test"})
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(request, timeout=5) as response:
            body = response.read()
            served_status = response.status
        report["restore"]["preview"]["served_status"] = served_status
        report["restore"]["preview"]["served_sha256"] = hashlib.sha256(body).hexdigest()
        report["restore"]["preview"]["live_index_sha256"] = sha256(pages_index_html)
        report["restore"]["preview"]["served_matches_live"] = hashlib.sha256(body).hexdigest() == sha256(pages_index_html)
        require(served_status == 200, "restore_served_status", served_status)
        require(report["restore"]["preview"]["served_matches_live"], "restore_live_bytes_mismatch",
                report["restore"]["preview"]["served_sha256"])
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        log.close()

    # Tunnel DB/identity offline: restore and validate as its service account.
    tunnel_restore = restore / "tunnel"
    tunnel_restore.mkdir(mode=0o700)
    tunnel_user = pwd.getpwnam("switchboard-tunnel")
    os.chown(tunnel_restore, tunnel_user.pw_uid, tunnel_user.pw_gid)
    tunnel_seed = tunnel_restore / "tunnel-state.backup.db"
    shutil.copy2(tunnel_db, tunnel_seed)
    os.chown(tunnel_seed, tunnel_user.pw_uid, tunnel_user.pw_gid)
    os.chmod(tunnel_seed, 0o600)
    result = run([PREVIEW, "db", "restore", "-from", tunnel_seed, "-to", tunnel_restore / "state.db"],
                 user="switchboard-tunnel", check=False)
    require(result.returncode == 0, "restore_tunnel_db", result.returncode)
    report["restore"]["tunnel"] = {"restore_exit": result.returncode}
    verify = sqlite_report(tunnel_restore / "state.db")
    require(verify["integrity"] == "ok", "restore_tunnel_integrity", verify)
    report["restore"]["tunnel"]["verify"] = verify
    target = tun_dst / report["tunnel_identity"]["current"]
    require(target.is_dir(), "restore_tunnel_identity_missing", str(target))
    copy_tree(target, tunnel_restore / "identity", record=False)
    chain = cert_chain(tunnel_restore / "identity/ca.crt", tunnel_restore / "identity/edge.crt")
    match = key_cert_match(tunnel_restore / "identity/edge.crt", tunnel_restore / "identity/edge.key")
    require(chain, "restore_tunnel_chain", str(tunnel_restore / "identity"))
    require(match, "restore_tunnel_key_cert", str(tunnel_restore / "identity"))
    report["restore"]["tunnel"]["identity_chain"] = chain
    report["restore"]["tunnel"]["identity_key_match"] = match
    access = {}
    for label, path in (("db", tunnel_restore / "state.db"),
                        ("identity_cert", tunnel_restore / "identity/edge.crt"),
                        ("identity_key", tunnel_restore / "identity/edge.key"),
                        ("identity_ca", tunnel_restore / "identity/ca.crt")):
        check = run(["test", "-r", path], user="switchboard-tunnel", check=False)
        access[label] = check.returncode == 0
        require(access[label], "restore_access_denied", {"as": "switchboard-tunnel", "path": str(path)})
    report["restore"]["tunnel"]["service_access"] = access
    tunnel_token = tunnel_restore / "admin.token"
    tunnel_token.write_text(secrets.token_hex(32) + "\n")
    os.chown(tunnel_token, tunnel_user.pw_uid, tunnel_user.pw_gid)
    os.chmod(tunnel_token, 0o600)
    tunnel_config = tunnel_restore / "restore-tunnel.json"
    tunnel_config.write_text(json.dumps({
        "listen": "127.0.0.1:%d" % free_port(),
        "admin": {"listen": "127.0.0.1:%d" % free_port(), "socket": str(tunnel_restore / "admin.sock"),
                  "token": "file:" + str(tunnel_token)},
        "tunnel": {"listen": "127.0.0.1:%d" % free_port(),
                   "cert_file": str(tunnel_restore / "identity/edge.crt"),
                   "key_file": str(tunnel_restore / "identity/edge.key"),
                   "ca_file": str(tunnel_restore / "identity/ca.crt")},
        "routes": [{"host": "restore.test", "connector": "portfolio"}],
    }))
    os.chown(tunnel_config, tunnel_user.pw_uid, tunnel_user.pw_gid)
    os.chmod(tunnel_config, 0o600)
    validate = run([PREVIEW, "validate", "-config", tunnel_config], user="switchboard-tunnel", check=False)
    require(validate.returncode == 0, "restore_tunnel_config_validate", validate.returncode)
    report["restore"]["tunnel"]["config_validate_exit"] = validate.returncode
    report["restore"]["tls"] = [{"host": entry["host"], "generation": entry["generation"],
                                 "key_cert_match": entry["key_cert_match"]}
                                for entry in report["tls"]["certs"]]

    # Coherence: compare the live mutable state at the start and at the end.
    after = live_fingerprint()
    report["coherence"]["after"] = after
    report["coherence"]["coherent"] = report["coherence"]["before"] == after
    if args.quiesced:
        require(report["coherence"]["coherent"], "state_changed_during_quiesced_capture",
                {"before": report["coherence"]["before"], "after": after})

    # The capture tool must not have changed the units' state.
    for unit in ("switchboard-preview.service", "switchboard-tunnel-edge.service"):
        state = run(["systemctl", "is-active", unit], check=False).stdout.strip()
        report["checks"].append({"name": "unit_after_capture", "unit": unit, "state": state})
        expected = "inactive" if args.quiesced else "active"
        require(state == expected, "unit_state_changed_during_capture", {"unit": unit, "state": state})

    # A live capture cannot be the final cutover checkpoint; only a quiesced
    # run with coherent state is complete. Both states still fail closed.
    status = "complete" if (args.quiesced and report["coherence"]["coherent"]) else "preliminary"
    final_status = finalize(root, index, report, args, [], status)
    if final_status != "complete":
        print(json.dumps({"status": final_status, "private": str(root), "index": str(index),
                          "reason": ("live capture; rerun with --quiesced in the authorized window"
                                     if final_status == "preliminary" else
                                     "mandatory check failed; see INCOMPLETE.json")}, indent=2))
        raise SystemExit(1 if final_status == "incomplete" else 3)


def finalize(root, index, report, args, failures, status):
    state = CURRENT
    if state.get("finalized") or state.get("finalizing"):
        return
    state["finalizing"] = True
    failures = list(failures) + [item for item in state.get("failures", []) if item not in failures]
    if failures:
        status = "incomplete"
    if status != "incomplete":
        # Ownership is a mandatory check: every copied resource must still
        # carry the source UID/GID/mode (or a recorded symlink target).
        mismatches = []
        for rel, source in state.get("source_meta", {}).items():
            path = root / rel
            if not path.exists() and not path.is_symlink():
                mismatches.append({"path": rel, "error": "missing"})
                continue
            backup = file_metadata(path)
            for field in ("uid", "gid", "mode", "type", "target"):
                if source.get(field) != backup.get(field):
                    mismatches.append({"path": rel, "field": field, "source": source.get(field),
                                       "backup": backup.get(field)})
        report["inventory_mismatches"] = mismatches
        if mismatches:
            failures.append({"check": "ownership_preserved", "detail": mismatches[:20]})
            status = "incomplete"

    report["status"] = status
    report["failures"] = failures
    report["failed"] = status == "incomplete"

    inventory = []
    for current_root, dirs, files in os.walk(root, followlinks=False):
        for name in sorted(dirs + files):
            path = Path(current_root) / name
            if path.name in ("manifest.json", "inventory.tsv", "SHA256SUMS", "INCOMPLETE.json",
                             "PRELIMINARY.md", "ROLLBACK.md"):
                continue
            rel = str(path.relative_to(root))
            st = path.lstat()
            source = state.get("source_meta", {}).get(rel)
            backup = file_metadata(path)
            row = {
                "path": rel,
                "type": backup["type"],
                "mode": f"{stat.S_IMODE(st.st_mode):04o}",
                "owner": owner_name(st.st_uid),
                "group": group_name(st.st_gid),
                "uid": st.st_uid,
                "gid": st.st_gid,
                "size": st.st_size,
                "source_kind": "copied" if source else "created",
                "source_uid": source.get("uid") if source else None,
                "source_gid": source.get("gid") if source else None,
                "source_mode": f"{source.get('mode'):04o}" if source and source.get("mode") is not None else None,
                "backup_uid": backup["uid"],
                "backup_gid": backup["gid"],
                "backup_mode": f"{backup['mode']:04o}",
                "match": (source is None or all(source.get(f) == backup.get(f)
                                                 for f in ("uid", "gid", "mode", "type", "target"))),
            }
            if backup["type"] == "symlink":
                row["target"] = backup.get("target")
                row["sha256"] = hashlib.sha256(backup.get("target", "").encode()).hexdigest()
            elif backup["type"] == "file":
                row["sha256"] = sha256(path)
            else:
                row["sha256"] = ""
            inventory.append(row)

    (root / "manifest.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    os.chmod(root / "manifest.json", 0o600)
    with (root / "inventory.tsv").open("w") as handle:
        columns = ("path", "type", "source_kind", "source_uid", "source_gid", "source_mode",
                   "backup_uid", "backup_gid", "backup_mode", "owner", "group", "size", "match", "sha256", "target")
        handle.write("\t".join(columns) + "\n")
        for row in sorted(inventory, key=lambda item: item["path"]):
            handle.write("\t".join(str(row.get(column, "")) for column in columns) + "\n")
    os.chmod(root / "inventory.tsv", 0o600)
    with (root / "SHA256SUMS").open("w") as handle:
        for row in sorted(inventory, key=lambda item: item["path"]):
            if row["type"] == "file":
                handle.write(f"{row['sha256']}  {row['path']}\n")
    os.chmod(root / "SHA256SUMS", 0o600)

    public = redact(report)
    public["configs"] = [c for c in public.get("configs", [])
                         if not c.get("source", "").endswith(".token")
                         and not str(c.get("path", "")).startswith("config/secret-")]
    public["secrets"] = [{"name": Path(s["path"]).name, "mode": s["mode"]} for s in report["secrets"]]
    if "edge.key" in public.get("tunnel_identity", {}):
        public["tunnel_identity"]["edge.key"]["sha256"] = "<redacted>"
    index_manifest = index / "manifest-public.json"
    index_manifest.write_text(json.dumps(public, indent=2, sort_keys=True) + "\n")
    os.chmod(index_manifest, 0o600)
    if not args.readable_only:
        octavio = pwd.getpwnam("octavio")
        os.chown(index_manifest, octavio.pw_uid, octavio.pw_gid)

    if status == "complete":
        path = index / "ROLLBACK.md"
        path.write_text(rollback_text(root, report) + "\n")
    elif status == "preliminary":
        path = index / "PRELIMINARY.md"
        path.write_text(preliminary_text(root, report) + "\n")
        (root / "PRELIMINARY.md").write_text(preliminary_text(root, report) + "\n")
        os.chmod(root / "PRELIMINARY.md", 0o600)
    else:
        path = index / "INCOMPLETE.json"
        path.write_text(json.dumps({"status": status, "private": str(root),
                                    "failures": failures}, indent=2, sort_keys=True) + "\n")
        (root / "INCOMPLETE.json").write_text(json.dumps({"status": status, "failures": failures},
                                                         indent=2, sort_keys=True) + "\n")
        os.chmod(root / "INCOMPLETE.json", 0o600)
    os.chmod(path, 0o600)
    if not args.readable_only:
        octavio = pwd.getpwnam("octavio")
        os.chown(path, octavio.pw_uid, octavio.pw_gid)
    state["finalized"] = True
    state["finalizing"] = False
    return status


def preliminary_text(root, report):
    coherence = report.get("coherence", {})
    return f"""# REL-013/017 preliminary edge capture

Checkpoint: `{root}`

This is a **preliminary** capture, not the final cutover checkpoint. The
services were running while it was taken or their mutable state changed.
`coherence.coherent={coherence.get('coherent')}`.

Use it only to inspect a restore. Before the coordinated change, repeat the
capture with `--quiesced` after stopping the connector and the two edges in the
authorized window. Do not use this directory as the paired rollback checkpoint.
No `ROLLBACK.md` is emitted for a preliminary capture.

The mandatory checks that did pass are recorded in `manifest.json`; a missing
resource ends the run as `incomplete`, not `preliminary`.
"""


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def rollback_text(root, report):
    preview = next((b for b in report["binaries"] if b["source"] == str(PREVIEW)), {})
    tunnel = next((b for b in report["binaries"] if b["source"] == str(TUNNEL)), {})
    return f"""# REL-013/017 rollback checkpoint

Checkpoint: `{root}`

This directory is private. Do not copy it to Git or to another host without a
new authorization.

## Previous artifacts

- Preview edge: `{preview.get('source')}` sha256 `{preview.get('sha256')}`
- Tunnel edge: `{tunnel.get('source')}` sha256 `{tunnel.get('sha256')}`
- SQLite snapshots: `db/preview-state.db`, `db/tunnel-state.db`
- Pages: `pages/` (matching deployments metadata in `manifest.json`)
- Runtime: `runtime.json`
- TLS/ACME: `tls/`
- Tunnel identity: `tunnel/`
- Units and configs: `config/`, `systemd/`

## Rollback after a failed coordinated update

1. Stop the connector, then the candidate edges. Do not start the previous
   connector against a candidate edge.
2. Restore `db/preview-state.db` and `db/tunnel-state.db` to new paths with the
   previous binary's `db restore`; never overwrite the candidate database.
3. Restore `runtime.json`, `pages/`, `tls/` and `tunnel/` to the service paths
   with the service stopped, preserving owners and modes from `inventory.tsv`.
4. Reinstall the previous binaries from `bin/`, verify their sha256, and start
   the previous edges before the previous connector.
5. Reconcile revocations and repeat the tunnel, login, allowed/denied app and
   publication checks. Record the interruption, discarded writes and chosen
   checkpoint.

The capture method and coherence result are in `manifest.json`. A complete
checkpoint is only emitted for a quiesced run with coherent state; a live run
is marked preliminary and has no rollback document. The capture tool never
stopped, started or restarted an active unit.
"""


def cleanup_restore_roots():
    for path in CURRENT.get("cleanup", []):
        shutil.rmtree(path, ignore_errors=True)


if __name__ == "__main__":
    try:
        main()
    except SystemExit:
        cleanup_restore_roots()
        raise
    except BaseException as exc:
        state = CURRENT
        if state.get("root") is not None and state.get("report") is not None and not state.get("finalized"):
            state["failures"].append({"check": "unexpected_exception", "detail": type(exc).__name__})
            try:
                finalize(state["root"], state["index"], state["report"], state["args"],
                         state["failures"], "incomplete")
            except Exception:
                pass
        cleanup_restore_roots()
        raise
    else:
        cleanup_restore_roots()
