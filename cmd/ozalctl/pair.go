package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/ozal-project/ozal/internal/ctl"
	"github.com/ozal-project/ozal/internal/ctl/sshexec"
	"github.com/ozal-project/ozal/internal/release"
)

type pairRemote interface{ ctl.Remote }

type pairSSHNode struct {
	remote       pairRemote
	name         string
	role         string
	unit         string
	user         string
	binary       string
	stage        string
	lock         string
	config       string
	db           string
	keep         int
	socket       string
	job          string
	peer         string
	backup       string
	dbBackup     string
	previousHash string
}

func (n *pairSSHNode) Name() string { return n.name }

func (n *pairSSHNode) command(ctx context.Context, command string, stdin io.Reader) error {
	_, err := n.remote.Run(ctx, command, stdin)
	return err
}

func (n *pairSSHNode) run(ctx context.Context, command string, stdin io.Reader) (string, error) {
	return n.remote.Run(ctx, command, stdin)
}

func (n *pairSSHNode) root(ctx context.Context, command string) error {
	code, err := n.remote.Terminal(ctx, command)
	if err != nil {
		return fmt.Errorf("interactive sudo is required: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("remote root operation exited %d", code)
	}
	return nil
}

func (n *pairSSHNode) serviceCommand(action string) string {
	if n.role == "edge" {
		return "sudo systemctl " + action + " " + ctl.Quote(n.unit)
	}
	return "export XDG_RUNTIME_DIR=/run/user/$(id -u); systemctl --user " + action + " " + ctl.Quote(n.unit)
}

func (n *pairSSHNode) Acquire(ctx context.Context, owner string) error {
	out, err := n.run(ctx, ctl.LockAcquire(n.lock, owner), nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "taken" {
		return fmt.Errorf("another update holds %s (%s)", n.name, strings.TrimSpace(out))
	}
	return nil
}

func (n *pairSSHNode) Release(ctx context.Context, owner string) error {
	_, err := n.run(ctx, ctl.LockRelease(n.lock, owner), nil)
	return err
}

func (n *pairSSHNode) Inspect(ctx context.Context) (ctl.PairState, error) {
	var state ctl.PairState
	hash, err := n.run(ctx, "sha256sum "+ctl.Quote(n.binary), nil)
	if err != nil {
		return state, err
	}
	fields := strings.Fields(hash)
	if len(fields) == 0 {
		return state, errors.New("installed binary hash is missing")
	}
	state.SHA256 = fields[0]
	version, err := n.run(ctx, ctl.Quote(n.binary)+" version -json", nil)
	if err != nil {
		return state, err
	}
	var info struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		OS      string `json:"os"`
		Arch    string `json:"arch"`
	}
	if err := json.Unmarshal([]byte(version), &info); err != nil {
		return state, errors.New("installed binary did not report a version")
	}
	state.Version = info.Version
	if state.Version == "" || state.Version == "dev" || state.Version == "(devel)" {
		state.Version = info.Commit
	}
	if info.OS != "linux" || info.Arch != "amd64" {
		return state, fmt.Errorf("%s reports %s/%s; this coordinator builds one linux/amd64 candidate for the pair", n.name, info.OS, info.Arch)
	}
	if n.role == "edge" {
		if err := n.root(ctx, n.serviceCommand("is-active --quiet")); err != nil {
			return state, fmt.Errorf("edge service is not active: %w", err)
		}
	} else if err := n.command(ctx, n.serviceCommand("is-active --quiet"), nil); err != nil {
		return state, fmt.Errorf("connector service is not active: %w", err)
	}
	return state, nil
}

func (n *pairSSHNode) Stage(ctx context.Context, bin ctl.Binary, want string) error {
	if _, err := n.run(ctx, "umask 077; mkdir -p "+ctl.Quote(n.stage), nil); err != nil {
		return err
	}
	var final, receive string
	if n.role == "edge" {
		part, staged := ctl.StageFile(ctl.Edge{Stage: n.stage})
		final = staged
		receive = fmt.Sprintf("umask 077 && rm -f %s && gzip -dc > %s && chmod 700 %s && mv -f %s %s && sha256sum %s", ctl.Quote(part), ctl.Quote(part), ctl.Quote(part), ctl.Quote(part), ctl.Quote(final), ctl.Quote(final))
	} else {
		part, staged := n.stage+"/ozal-connector.next.part", n.stage+"/ozal-connector.next"
		final = staged
		receive = fmt.Sprintf("umask 077 && rm -f %s && gzip -dc > %s && chmod 700 %s && mv -f %s %s && sha256sum %s", ctl.Quote(part), ctl.Quote(part), ctl.Quote(part), ctl.Quote(part), ctl.Quote(final), ctl.Quote(final))
	}
	packed, err := pairGzip(bin.Data)
	if err != nil {
		return err
	}
	reply, err := n.run(ctx, receive, bytes.NewReader(packed))
	if err != nil {
		return err
	}
	fields := strings.Fields(reply)
	if len(fields) == 0 || fields[0] != want {
		return errors.New("staged binary hash differs from the candidate")
	}
	return nil
}

func (n *pairSSHNode) Checkpoint(ctx context.Context, state ctl.PairState) error {
	if len(state.SHA256) < 12 {
		return errors.New("invalid previous binary hash")
	}
	n.backup = n.binary + ".pair-" + n.job + ".bak"
	n.previousHash = state.SHA256
	if n.role == "connector" {
		command := "set -eu; test ! -e " + ctl.Quote(n.backup) + " && test ! -L " + ctl.Quote(n.backup) + "; cp -p " + ctl.Quote(n.binary) + " " + ctl.Quote(n.backup) + "; test \"$(sha256sum " + ctl.Quote(n.backup) + " | cut -d' ' -f1)\" = " + ctl.Quote(state.SHA256)
		return n.command(ctx, command, nil)
	}
	if n.db != "" {
		n.dbBackup = n.db + ".pair-" + n.job + ".bak.db"
	}
	old := ctl.Quote(n.binary)
	command := "set -eu; test ! -e " + ctl.Quote(n.backup) + " && test ! -L " + ctl.Quote(n.backup) + "; cp -p " + old + " " + ctl.Quote(n.backup) + "; test \"$(sha256sum " + ctl.Quote(n.backup) + " | cut -d' ' -f1)\" = " + ctl.Quote(state.SHA256)
	if n.dbBackup != "" {
		command += "; test ! -e " + ctl.Quote(n.dbBackup) + " && test ! -L " + ctl.Quote(n.dbBackup) + "; " + old + " db backup -db " + ctl.Quote(n.db) + " -out " + ctl.Quote(n.dbBackup)
	}
	return n.root(ctx, "sudo sh -c "+ctl.Quote(command))
}

func (n *pairSSHNode) Stop(ctx context.Context) error {
	if n.role == "edge" {
		return n.root(ctx, n.serviceCommand("stop"))
	}
	return n.command(ctx, n.serviceCommand("stop"), nil)
}

func (n *pairSSHNode) Install(ctx context.Context, want string) error {
	if n.role == "edge" {
		_, staged := ctl.StageFile(ctl.Edge{Stage: n.stage})
		code, err := n.remote.Terminal(ctx, ctl.PairUpgradeCommand(ctl.Edge{Unit: n.unit, ServiceUser: n.user, Binary: n.binary, Config: n.config, StateDB: n.db, Keep: n.keep}, staged, want))
		if err != nil {
			return fmt.Errorf("interactive sudo is required to install the edge candidate: %w", err)
		}
		return ctl.Outcome(code, ctl.Edge{Unit: n.unit}, path.Base(n.binary)+".bak-"+n.previousHash[:12])
	}
	staged := n.stage + "/ozal-connector.next"
	pattern := path.Join(path.Dir(n.binary), ".ozal-pair.XXXXXX")
	command := "set -eu; test -f " + ctl.Quote(n.binary) + " && test ! -L " + ctl.Quote(n.binary) + "; test \"$(sha256sum " + ctl.Quote(staged) + " | cut -d' ' -f1)\" = " + ctl.Quote(want) + "; tmp=$(mktemp " + ctl.Quote(pattern) + "); trap 'rm -f \"$tmp\"' EXIT; cp " + ctl.Quote(staged) + " \"$tmp\"; chmod 755 \"$tmp\"; test \"$(sha256sum \"$tmp\" | cut -d' ' -f1)\" = " + ctl.Quote(want) + "; mv -f \"$tmp\" " + ctl.Quote(n.binary)
	return n.command(ctx, command, nil)
}

func (n *pairSSHNode) Start(ctx context.Context) error {
	if n.role == "edge" {
		if err := n.root(ctx, "sudo systemctl reset-failed "+ctl.Quote(n.unit)+" && "+n.serviceCommand("start")); err != nil {
			return err
		}
		probe := "i=0; while [ \"$i\" -lt 30 ]; do if curl --silent --show-error --fail --max-time 2 --unix-socket " + ctl.Quote(n.socket) + " http://localhost/_/healthz >/dev/null; then exit 0; fi; i=$((i+1)); sleep 1; done; echo 'edge admin socket did not become ready' >&2; exit 1"
		return n.root(ctx, "sudo sh -c "+ctl.Quote(probe))
	}
	return n.command(ctx, n.serviceCommand("start"), nil)
}

func (n *pairSSHNode) Ready(ctx context.Context, expectedHash string) error {
	hash, err := n.run(ctx, "sha256sum "+ctl.Quote(n.binary), nil)
	if err != nil {
		return err
	}
	fields := strings.Fields(hash)
	if len(fields) == 0 || fields[0] != expectedHash {
		return errors.New("installed binary hash does not match the coordinated version")
	}
	if n.role == "edge" {
		if err := n.root(ctx, "sudo systemctl is-active --quiet "+ctl.Quote(n.unit)); err != nil {
			return err
		}
		probe := "curl --silent --show-error --fail --unix-socket " + ctl.Quote(n.socket) + " http://localhost/_/api/connectors/" + n.peer + "/ready | grep -q '\"ready\":true'"
		return n.root(ctx, "sudo sh -c "+ctl.Quote(probe))
	}
	return n.command(ctx, n.serviceCommand("is-active --quiet"), nil)
}

func (n *pairSSHNode) Restore(ctx context.Context, _ ctl.PairState) error {
	if n.role == "connector" {
		pattern := path.Join(path.Dir(n.binary), ".ozal-restore.XXXXXX")
		command := "set -eu; test -f " + ctl.Quote(n.binary) + " && test ! -L " + ctl.Quote(n.binary) + "; test -f " + ctl.Quote(n.backup) + " && test ! -L " + ctl.Quote(n.backup) + "; tmp=$(mktemp " + ctl.Quote(pattern) + "); trap 'rm -f \"$tmp\"' EXIT; cp " + ctl.Quote(n.backup) + " \"$tmp\"; chmod 755 \"$tmp\"; mv -f \"$tmp\" " + ctl.Quote(n.binary)
		return n.command(ctx, command, nil)
	}
	pattern := path.Join(path.Dir(n.binary), ".ozal-restore.XXXXXX")
	command := "set -eu; test -f " + ctl.Quote(n.binary) + " && test ! -L " + ctl.Quote(n.binary) + "; test -f " + ctl.Quote(n.backup) + " && test ! -L " + ctl.Quote(n.backup) + "; tmp=$(mktemp " + ctl.Quote(pattern) + "); trap 'rm -f \"$tmp\"' EXIT; cp -p " + ctl.Quote(n.backup) + " \"$tmp\"; mv -f \"$tmp\" " + ctl.Quote(n.binary)
	if n.dbBackup != "" {
		restored := n.db + ".restore-" + n.job
		failed := n.db + ".failed-" + n.job
		command += "; test -f " + ctl.Quote(n.dbBackup) + "; rm -f " + ctl.Quote(n.db+"-wal") + " " + ctl.Quote(n.db+"-shm") + "; mv " + ctl.Quote(n.db) + " " + ctl.Quote(failed) + "; " + ctl.Quote(n.binary) + " db restore -from " + ctl.Quote(n.dbBackup) + " -to " + ctl.Quote(restored) + "; chown " + ctl.Quote(n.user+":"+n.user) + " " + ctl.Quote(restored) + "; mv " + ctl.Quote(restored) + " " + ctl.Quote(n.db)
	}
	return n.root(ctx, "sudo sh -c "+ctl.Quote(command))
}

func (n *pairSSHNode) Report(ctx context.Context, p ctl.PairProgress) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	request := "curl --silent --show-error --max-time 5 --unix-socket " + ctl.Quote(n.socket) + " -H 'Content-Type: application/json' --data-binary " + ctl.Quote(string(raw)) + " -o /dev/null -w '%{http_code}' http://localhost/_/api/updates/status"
	accepted := "202"
	if p.Result == "current" || p.Phase == "staging" || p.Phase == "checkpointing" || p.Phase == "stopping" {
		accepted += "|404"
	}
	command := "if code=$(" + request + "); then case \"$code\" in " + accepted + ") exit 0;; *) echo \"update status API returned $code\" >&2; exit 1;; esac; else echo 'update status API did not answer' >&2; exit 1; fi"
	return n.root(ctx, "sudo sh -c "+ctl.Quote(command))
}

func pairGzip(data []byte) ([]byte, error) {
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func newPairJobID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "pair-" + time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(raw[:]), nil
}

type signedPairBuilder struct {
	api    string
	key    []byte
	client *http.Client
}

func (b signedPairBuilder) Build(ctx context.Context, ref string) (ctl.Binary, error) {
	key, err := release.ParsePublicKey(string(b.key))
	if err != nil {
		return ctl.Binary{}, err
	}
	data, _, err := release.FetchBinary(ctx, b.client, b.api, ref, "linux", "amd64", key)
	if err != nil {
		return ctl.Binary{}, err
	}
	return ctl.Binary{Data: data, Commit: ref}, nil
}

func edgePairNode(c *common, job, connector string) *pairSSHNode {
	e := c.target.Edge
	return &pairSSHNode{remote: c.remote, name: c.name + "-edge", role: "edge", unit: e.Unit, user: e.ServiceUser, binary: e.Binary, stage: e.Stage, lock: e.Lock, config: e.Config, db: e.StateDB, keep: e.Keep, socket: e.Socket, job: job, peer: connector}
}

func connectorPairNode(c *common, job string) *pairSSHNode {
	x := c.target.Connector
	remote := sshexec.Remote{Target: x.SSH.Target, Key: x.SSH.Key, Interactive: isTerminal(os.Stdin)}
	return &pairSSHNode{remote: remote, name: x.Name, role: "connector", unit: x.Unit, user: x.ServiceUser, binary: x.Binary, stage: x.Stage, lock: x.Lock, job: job}
}

func edgePairUpgrade(args []string) int {
	fs := flag.NewFlagSet("edge pair-upgrade", flag.ContinueOnError)
	ref := fs.String("ref", "", "signed semantic release tag to install, for example v0.2.0")
	publicKey := fs.String("public-key", "", "pinned Ed25519 release public key file")
	api := fs.String("api", "https://api.github.com/repos/ozal-project/ozal", "release API")
	apply := fs.Bool("apply", false, "do it (default: print the plan)")
	force := fs.Bool("force", false, "allow moving from an unversioned development build")
	c, ok := setup(fs, args)
	if !ok {
		return 2
	}
	if c.target.Connector == nil {
		return finish(errors.New("target.connector is required for a coordinated update"))
	}
	if *ref == "" || *publicKey == "" {
		fmt.Fprintln(os.Stderr, "ozalctl: -ref signed-tag and -public-key are required")
		return 2
	}
	if *apply && !isTerminal(os.Stdin) {
		return finish(errors.New("-apply requires an interactive terminal for sudo on the edge"))
	}
	key, err := os.ReadFile(*publicKey)
	if err != nil {
		return finish(fmt.Errorf("read release public key: %w", err))
	}
	job, err := newPairJobID()
	if err != nil {
		return finish(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	builder := signedPairBuilder{api: *api, key: key, client: &http.Client{Timeout: 5 * time.Minute}}
	edge := edgePairNode(c, job, c.target.Connector.Name)
	connector := connectorPairNode(c, job)
	err = ctl.UpdatePair(ctx, os.Stdout, builder, edge, connector, ctl.PairUpdateRequest{Ref: *ref, Owner: c.owner, JobID: job, Apply: *apply, Force: *force})
	return finish(err)
}
