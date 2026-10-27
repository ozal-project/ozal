package ctl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func goodTarget() Target {
	return Target{
		SSH: SSH{Target: "deploy@edge.example.net", Key: "~/.ssh/k"},
		Edge: Edge{
			Unit: "switchboard", ServiceUser: "switchboard", Binary: "/usr/local/lib/switchboard/switchboard",
			Config: "/etc/switchboard/routing.json", StateDB: "/var/lib/switchboard/state.db",
			Stage: "/home/deploy/stage", Lock: "/home/deploy/lock", CommandLink: "/usr/local/bin/switchboard",
		},
	}
}

func TestValidateRefusesUnsafeValues(t *testing.T) {
	for name, mutate := range map[string]func(*Target){
		"relative binary":  func(x *Target) { x.Edge.Binary = "bin/switchboard" },
		"dotdot path":      func(x *Target) { x.Edge.Config = "/etc/../tmp/x" },
		"newline in stage": func(x *Target) { x.Edge.Stage = "/a\nb" },
		"bad host":         func(x *Target) { x.SSH.Target = "host; reboot" },
		"bad unit":         func(x *Target) { x.Edge.Unit = "x; reboot" },
		"bad account":      func(x *Target) { x.Edge.ServiceUser = "Root" },
		"huge keep":        func(x *Target) { x.Edge.Keep = 99 },
	} {
		t.Run(name, func(t *testing.T) {
			target := goodTarget()
			mutate(&target)
			if target.Validate() == nil {
				t.Fatal("unsafe target accepted")
			}
		})
	}
	if err := goodTarget().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestParseTargetsIsStrict(t *testing.T) {
	_, err := ParseTargets(strings.NewReader(`{"targets":{"a":{"ssh":{"target":"u@h","key":"k"},"edge":{},"typo":1}}}`))
	if err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := ParseTargets(strings.NewReader(`{"targets":{}}`)); err == nil {
		t.Fatal("empty targets accepted")
	}
}

func TestPick(t *testing.T) {
	one := Targets{"a": goodTarget()}
	if n, _, err := one.Pick(""); err != nil || n != "a" {
		t.Fatalf("sole target not picked: %v", err)
	}
	two := Targets{"a": goodTarget(), "b": goodTarget()}
	if _, _, err := two.Pick(""); err == nil {
		t.Fatal("ambiguous pick accepted")
	}
	if _, _, err := two.Pick("c"); err == nil {
		t.Fatal("unknown target accepted")
	}
}

func TestQuoteSurvivesAShell(t *testing.T) {
	hostile := `a'b"c $(touch /tmp/x) ; \n`
	out, err := exec.Command("sh", "-c", "printf %s "+Quote(hostile)).Output()
	if err != nil || string(out) != hostile {
		t.Fatalf("quote broke: %q %v", out, err)
	}
}

func TestOutcomeMapsExitCodes(t *testing.T) {
	e := goodTarget().Edge
	if Outcome(0, e, "b") != nil {
		t.Fatal("0 must succeed")
	}
	for _, code := range []int{2, 3, 4, 255, 1} {
		if Outcome(code, e, "b") == nil {
			t.Fatalf("code %d reported success", code)
		}
	}
}

func TestUpgradeCommandCarriesTheLayout(t *testing.T) {
	cmd := UpgradeCommand(goodTarget().Edge, "/home/deploy/stage/switchboard.next", "abc")
	for _, want := range []string{"'-unit' 'switchboard'", "'-binary' '/usr/local/lib/switchboard/switchboard'", "'-keep' '3'", "'/usr/local/bin/switchboard'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("missing %s in %s", want, cmd)
		}
	}
}

type fakeBuilder struct {
	bin Binary
	err error
}

func (b fakeBuilder) Build(context.Context, string) (Binary, error) { return b.bin, b.err }

type fakeRemote struct {
	live      string
	lock      string // reply to the lock command
	uploadSHA string // hash the edge reports after an upload ("" = real)
	code      int
	ran       []string
	uploaded  bool
	terminal  bool
}

func (f *fakeRemote) Run(_ context.Context, command string, stdin io.Reader) (string, error) {
	f.ran = append(f.ran, command)
	switch {
	case strings.HasPrefix(command, "sha256sum '/usr/local/lib"):
		return f.live + "  /usr/local/lib/switchboard/switchboard\n", nil
	case strings.Contains(command, "mkdir '/home/deploy/lock'"):
		return f.lock, nil
	case strings.Contains(command, "gzip -dc"):
		f.uploaded = true
		_, _ = io.Copy(io.Discard, stdin)
		return f.uploadSHA + "  x\n", nil
	case strings.Contains(command, "validate -config"):
		return "config valid\n", nil
	}
	return "", nil
}

func (f *fakeRemote) Terminal(_ context.Context, command string) (int, error) {
	f.ran = append(f.ran, "TERMINAL "+command)
	if !f.terminal {
		return 0, errors.New("no terminal")
	}
	return f.code, nil
}

func newRun(apply bool) UpgradeRequest {
	return UpgradeRequest{Name: "a", Target: goodTarget(), Ref: "HEAD", Apply: apply, Owner: "me"}
}

var data = []byte("binary")

func TestPlanOnlyChangesNothing(t *testing.T) {
	r := &fakeRemote{live: "old"}
	var out bytes.Buffer
	if err := Upgrade(context.Background(), &out, r, fakeBuilder{bin: Binary{Data: data, Commit: "abc1234"}}, newRun(false)); err != nil {
		t.Fatal(err)
	}
	if len(r.ran) != 1 || r.uploaded {
		t.Fatalf("plan touched the edge: %v", r.ran)
	}
}

func TestSameBinaryIsANoOp(t *testing.T) {
	r := &fakeRemote{live: sha(data)}
	if err := Upgrade(context.Background(), io.Discard, r, fakeBuilder{bin: Binary{Data: data, Commit: "c"}}, newRun(true)); err != nil {
		t.Fatal(err)
	}
	if len(r.ran) != 1 {
		t.Fatalf("no-op ran more: %v", r.ran)
	}
}

func TestBusyLockStopsBeforeUpload(t *testing.T) {
	r := &fakeRemote{live: "old", lock: "publish-site pid 9"}
	err := Upgrade(context.Background(), io.Discard, r, fakeBuilder{bin: Binary{Data: data, Commit: "c"}}, newRun(true))
	if !errors.Is(err, ErrBusy) || r.uploaded {
		t.Fatalf("err=%v uploaded=%v", err, r.uploaded)
	}
}

func TestApplyOrderAndLockRelease(t *testing.T) {
	r := &fakeRemote{live: "old", lock: "taken\n", uploadSHA: sha(data), terminal: true}
	if err := Upgrade(context.Background(), io.Discard, r, fakeBuilder{bin: Binary{Data: data, Commit: "c"}}, newRun(true)); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range r.ran {
		for _, key := range []string{"mkdir '/home/deploy/lock'", "gzip -dc", "validate -config", "TERMINAL", "rmdir"} {
			if strings.Contains(c, key) {
				order = append(order, key)
			}
		}
	}
	if strings.Join(order, ",") != "mkdir '/home/deploy/lock',gzip -dc,validate -config,TERMINAL,rmdir" {
		t.Fatalf("order: %v", order)
	}
}

func TestUploadMismatchIsRefusedAndLockReleased(t *testing.T) {
	r := &fakeRemote{live: "old", lock: "taken", uploadSHA: "bad", terminal: true}
	err := Upgrade(context.Background(), io.Discard, r, fakeBuilder{bin: Binary{Data: data, Commit: "c"}}, newRun(true))
	if err == nil {
		t.Fatal("mismatch accepted")
	}
	for _, c := range r.ran {
		if strings.HasPrefix(c, "TERMINAL") {
			t.Fatal("cutover ran after a mismatch")
		}
	}
	if !strings.Contains(r.ran[len(r.ran)-1], "rmdir") {
		t.Fatal("lock not released")
	}
}

func TestRollbackIsAnError(t *testing.T) {
	r := &fakeRemote{live: "old", lock: "taken", uploadSHA: sha(data), terminal: true, code: ExitRolledBack}
	if Upgrade(context.Background(), io.Discard, r, fakeBuilder{bin: Binary{Data: data, Commit: "c"}}, newRun(true)) == nil {
		t.Fatal("rollback reported as success")
	}
}

func TestApplyWithoutTerminalIsNotReportedAsSuccess(t *testing.T) {
	r := &fakeRemote{live: "old", lock: "taken", uploadSHA: sha(data)}
	err := Upgrade(context.Background(), io.Discard, r, fakeBuilder{bin: Binary{Data: data, Commit: "c"}}, newRun(true))
	if !errors.Is(err, ErrTerminalRequired) {
		t.Fatalf("missing terminal reported as %v", err)
	}
	if !strings.Contains(r.ran[len(r.ran)-1], "rmdir") {
		t.Fatal("lock not released after staging without a terminal")
	}
}

func runRoot(t *testing.T, dir, sum, link string, fakeExit string) (string, int) {
	t.Helper()
	fake := dir + "/sw"
	cmd := exec.Command("sh", "-c", RootScript, "upgrade", fake, sum, link, dir+"/real", "/etc/x/routing.json", "-unit", "u")
	cmd.Env = append(cmd.Environ(), "TMPROOT="+dir, "FAKE_EXIT="+fakeExit)
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	}
	return string(out), code
}

func TestRootScriptChecksHashRunsCopyAndLinks(t *testing.T) {
	dir := t.TempDir()
	body := "#!/bin/sh\necho \"fake upgrade $@\"; exit ${FAKE_EXIT:-0}\n"
	if err := os.WriteFile(dir+"/sw", []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha([]byte(body))

	out, code := runRoot(t, dir, "deadbeef", "-", "0")
	if code != ExitRefused || strings.Contains(out, "fake upgrade") {
		t.Fatalf("wrong hash must refuse without running: %d %s", code, out)
	}
	out, code = runRoot(t, dir, sum, dir+"/link", "0")
	if code != 0 || !strings.Contains(out, "fake upgrade upgrade -sha256 "+sum+" -unit u") {
		t.Fatalf("expected a run: %d %s", code, out)
	}
	wrapper, err := os.ReadFile(dir + "/link")
	if err != nil || !strings.Contains(string(wrapper), "managed by sbctl") ||
		!strings.Contains(string(wrapper), "/etc/x/routing.json") || !strings.Contains(string(wrapper), "exec "+dir+"/real") {
		t.Fatalf("wrapper not written: %v %q", err, wrapper)
	}
	if err := os.WriteFile(dir+"/foreign", []byte("mine"), 0o755); err != nil {
		t.Fatal(err)
	}
	runRoot(t, dir, sum, dir+"/foreign", "0")
	if got, _ := os.ReadFile(dir + "/foreign"); string(got) != "mine" {
		t.Fatal("a file sbctl did not write was replaced")
	}
	os.Remove(dir + "/link")
	if _, code := runRoot(t, dir, sum, dir+"/link", "3"); code != 3 {
		t.Fatalf("exit status must pass through, got %d", code)
	}
	if _, err := os.Lstat(dir + "/link"); err == nil {
		t.Fatal("link created after a failed upgrade")
	}
}
