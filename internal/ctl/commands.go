package ctl

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// Quote makes s one safe shell word.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Exit codes of the root wrapper, as `switchboard upgrade` defines them.
const (
	ExitUpgraded       = 0
	ExitRefused        = 2 // refused before the stop: the edge was not touched
	ExitRolledBack     = 3 // the new binary failed; the previous one is active
	ExitRollbackFailed = 4 // the new binary failed and the rollback is down too
)

// RootScript runs on the edge as root. Root first copies the staged file into
// its own directory and checks its hash there; only that copy runs, so the SSH
// user's files never execute as root unchecked. SIGHUP is ignored so a dropped
// session cannot stop a cutover halfway. Arguments: staged path, sha256, the
// command link ("-" for none), the installed binary path, the config path, then the flags for `switchboard upgrade`.
const RootScript = `
set -u
trap '' HUP
STAGED=$1 SHA=$2 LINK=$3 BINARY=$4 CONFIG=$5
shift 5
T=$(mktemp -d "${TMPROOT:-/root}/sbupgrade.XXXXXX") || exit 2
cp "$STAGED" "$T/switchboard" && chmod 755 "$T/switchboard" || { rm -rf "$T"; exit 2; }
if [ "$(sha256sum "$T/switchboard" | cut -d' ' -f1)" != "$SHA" ]; then
  echo "    the staged binary changed after its upload: refused"
  rm -rf "$T"
  exit 2
fi
"$T/switchboard" upgrade -sha256 "$SHA" "$@"
code=$?
rm -rf "$T"
if [ "$code" -eq 0 ] && [ "$LINK" != "-" ]; then
  # The command is a tiny wrapper that knows this host's config, so
  # "sudo switchboard console enrol" works from any directory. A file this
  # tool did not write is never replaced.
  if [ ! -e "$LINK" ] || [ -L "$LINK" ] || grep -q '^# managed by sbctl' "$LINK" 2>/dev/null; then
    W=$(mktemp "$(dirname "$LINK")/.switchboard.XXXXXX") &&
    printf '#!/bin/sh\n# managed by sbctl\nSWITCHBOARD_CONFIG=${SWITCHBOARD_CONFIG:-%s}\nexport SWITCHBOARD_CONFIG\nexec %s "$@"\n' "$CONFIG" "$BINARY" > "$W" &&
    chmod 755 "$W" && mv -f "$W" "$LINK" && echo "    command $LINK runs $BINARY with $CONFIG"
  fi
fi
exit $code
`

// UpgradeCommand is the one command run with the operator's terminal, so sudo
// can ask for its password once.
func UpgradeCommand(e Edge, staged, sha string) string {
	keep := e.Keep
	if keep == 0 {
		keep = 3
	}
	link := "-"
	if e.CommandLink != "" {
		link = e.CommandLink
	}
	args := []string{
		staged, sha, link, e.Binary, e.Config,
		"-unit", e.Unit, "-binary", e.Binary, "-config", e.Config,
		"-db", e.StateDB, "-user", e.ServiceUser, "-keep", strconv.Itoa(keep),
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = Quote(a)
	}
	return "sudo sh -c " + Quote(RootScript) + " upgrade " + strings.Join(quoted, " ")
}

// Outcome turns the wrapper's exit status into the sentence the operator reads.
func Outcome(code int, e Edge, backup string) error {
	switch code {
	case ExitUpgraded:
		return nil
	case ExitRefused:
		return fmt.Errorf("refused before the stop (staged binary changed, config refused or no sudo): the edge was not touched")
	case ExitRolledBack:
		return fmt.Errorf("the new binary failed; the edge rolled back to %s and is active again", backup)
	case ExitRollbackFailed:
		return fmt.Errorf("the new binary failed and the rollback did not come up: check journalctl -u %s", e.Unit)
	default:
		return fmt.Errorf("cutover ended with status %d (255: SSH failed); check %s and %s by hand", code, e.Unit, path.Dir(e.Binary))
	}
}

// BackupName is the file `switchboard upgrade` keeps for the binary it replaced.
func BackupName(liveSHA string) string {
	if len(liveSHA) > 12 {
		liveSHA = liveSHA[:12]
	}
	return "switchboard.bak-" + liveSHA
}

// StageFile is where an upload waits; the .part name means "incomplete".
func StageFile(e Edge) (part, final string) {
	return e.Stage + "/switchboard.next.part", e.Stage + "/switchboard.next"
}

// ReceiveCommand decompresses stdin into the .part file and renames it only
// once complete, so a cut upload never leaves a truncated binary staged.
func ReceiveCommand(e Edge) string {
	part, final := StageFile(e)
	return fmt.Sprintf("umask 077 && rm -f %[1]s && gzip -dc > %[1]s && chmod 700 %[1]s && mv -f %[1]s %[2]s && sha256sum %[2]s", Quote(part), Quote(final))
}

// LockAcquire is one atomic mkdir on the edge: it prints "taken" or the owner
// of the lock that is in the way. The format matches the lock the Python
// deployment scripts use, so both kinds exclude each other.
func LockAcquire(lock, owner string) string {
	p, o := Quote(lock), Quote(owner)
	return fmt.Sprintf("if mkdir %[1]s 2>/dev/null; then printf '%%s\\n' %[2]s > %[1]s/owner && echo taken; else cat %[1]s/owner 2>/dev/null || echo unknown owner; fi", p, o)
}

// LockRelease drops the lock only if it is still ours.
func LockRelease(lock, owner string) string {
	p, o := Quote(lock), Quote(owner)
	return fmt.Sprintf("if [ \"$(cat %[1]s/owner 2>/dev/null)\" = %[2]s ]; then rm -f %[1]s/owner && rmdir %[1]s; fi", p, o)
}
