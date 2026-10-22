package tunnelrenew

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

func nonce() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func command(ctx context.Context, input []byte, program string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s command failed: %w", filepath.Base(program), err)
	}
	return output, nil
}

// edgeConnectorArgs asks the running edge, over the admin socket named in
// its config (CLI-001), for the registry change: the rotation is applied by
// the process that owns the registry and published as connector.rotated.
// The edge must be running, which the renewal's probe already requires.
func edgeConnectorArgs(config, sub string, extra ...string) []string {
	args := []string{"connector", sub, "-config", config, "-name", "portfolio"}
	return append(args, extra...)
}

// Agent serves one role's identity on its host. Without Dir it uses the
// user-unit layout under Home. With Dir (postura 2: a system unit run by
// its own account) the identities live in Dir, Config names the edge's
// config, Binary is the edge binary itself and Unit the system unit, which
// the agent restarts through one exact sudo rule.
type Agent struct {
	Home, Role                string
	Dir, Config, Binary, Unit string
}

func (a Agent) base() string {
	if a.Dir != "" {
		return a.Dir
	}
	return filepath.Join(a.Home, ".local/share/switchboard-tunnel-"+a.Role)
}

func (a Agent) config() string {
	if a.Config != "" {
		return a.Config
	}
	return filepath.Join(a.base(), "config.integrated.json")
}

func (a Agent) binary() string {
	if a.Binary != "" {
		return a.Binary
	}
	return filepath.Join(a.base(), "switchboard")
}

// restartCommand is what the agent runs to restart its role's service.
func (a Agent) restartCommand() []string {
	if a.Unit != "" {
		// Exactly the sudoers rule's command: no flags of sudo's own beyond -n.
		return []string{"sudo", "-n", "/usr/bin/systemctl", "restart", a.Unit}
	}
	return []string{"systemctl", "--user", "restart", "switchboard-tunnel-" + a.Role + ".service"}
}

var versionPattern = regexp.MustCompile(`^identities/[0-9a-f]{32}$`)
var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (a Agent) target(link string) (string, error) {
	if link != "identity" && !versionPattern.MatchString(link) {
		return "", errors.New("invalid identity version")
	}
	path := filepath.Join(a.base(), link)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("identity version must be a real directory")
	}
	return path, nil
}

func (a Agent) current() (string, error) {
	link, err := os.Readlink(filepath.Join(a.base(), "current"))
	if err != nil {
		return "", err
	}
	_, err = a.target(link)
	return link, err
}

func (a Agent) Handle(ctx context.Context, request Request) (Reply, error) {
	var reply Reply
	if a.Role != "edge" && a.Role != "connector" {
		return reply, errors.New("invalid role")
	}
	base := a.base()
	switch request.Action {
	case "bootstrap":
		pointer := filepath.Join(base, "current")
		if _, err := os.Lstat(pointer); os.IsNotExist(err) {
			if _, err := a.target("identity"); err != nil {
				return reply, err
			}
			if err := os.Symlink("identity", pointer); err != nil {
				return reply, err
			}
			if err := syncDirectory(base); err != nil {
				return reply, err
			}
		}
		return a.Handle(ctx, Request{Action: "status"})
	case "status":
		link, err := a.current()
		if err != nil {
			return reply, err
		}
		dir, err := a.target(link)
		if err != nil {
			return reply, err
		}
		reply.Identity, err = certificate(filepath.Join(dir, a.Role+".crt"))
		reply.Identity.Link = link
		return reply, err
	case "prepare":
		if !idPattern.MatchString(request.ID) {
			return reply, errors.New("invalid transaction ID")
		}
		versions := filepath.Join(base, "identities")
		if err := os.MkdirAll(versions, 0700); err != nil {
			return reply, err
		}
		dir := filepath.Join(versions, request.ID)
		if err := os.Mkdir(dir, 0700); err != nil {
			return reply, err
		}
		csr, err := generate(a.Role, dir)
		if err != nil {
			return reply, err
		}
		for _, path := range []string{dir, versions, base} {
			if err := syncDirectory(path); err != nil {
				return reply, err
			}
		}
		return Reply{CSR: csr, Identity: Identity{Link: "identities/" + request.ID}}, nil
	case "certify":
		dir, err := a.target(request.Link)
		if err != nil {
			return reply, err
		}
		current, err := a.current()
		if err != nil {
			return reply, err
		}
		previous, err := a.target(current)
		if err != nil {
			return reply, err
		}
		if err := writeNew(filepath.Join(dir, a.Role+".crt"), []byte(request.Certificate)); err != nil {
			return reply, err
		}
		ca, err := os.ReadFile(filepath.Join(previous, "ca.crt"))
		if err != nil {
			return reply, err
		}
		if err := writeNew(filepath.Join(dir, "ca.crt"), ca); err != nil {
			return reply, err
		}
		if err := validate(a.Role, dir, filepath.Join(previous, "ca.crt"), time.Now()); err != nil {
			return reply, err
		}
		if err := syncDirectory(dir); err != nil {
			return reply, err
		}
		reply.Identity, err = certificate(filepath.Join(dir, a.Role+".crt"))
		reply.Identity.Link = request.Link
		return reply, err
	case "switch":
		current, err := a.current()
		if err != nil {
			return reply, err
		}
		if current != request.Expected {
			return reply, errors.New("identity pointer changed outside this renewal")
		}
		if _, err := a.target(request.Link); err != nil {
			return reply, err
		}
		id, err := nonce()
		if err != nil {
			return reply, err
		}
		temporary := filepath.Join(base, ".current-"+id)
		if err := os.Symlink(request.Link, temporary); err != nil {
			return reply, err
		}
		defer os.Remove(temporary)
		if err := os.Rename(temporary, filepath.Join(base, "current")); err != nil {
			return reply, err
		}
		return reply, syncDirectory(base)
	case "restart":
		restart := a.restartCommand()
		_, err := command(ctx, nil, restart[0], restart[1:]...)
		return reply, err
	case "active":
		if a.Role != "edge" {
			break
		}
		output, err := command(ctx, nil, a.binary(), edgeConnectorArgs(a.config(), "current")...)
		reply.Active = string(bytes.TrimSpace(output))
		return reply, err
	case "rotate":
		if a.Role != "edge" {
			break
		}
		dir, err := os.MkdirTemp(base, "renew-public-")
		if err != nil {
			return reply, err
		}
		defer os.RemoveAll(dir)
		cert := filepath.Join(dir, "replacement.crt")
		if err := writeNew(cert, []byte(request.Certificate)); err != nil {
			return reply, err
		}
		_, err = command(ctx, nil, a.binary(), edgeConnectorArgs(a.config(), "rotate", "-expected", request.Expected, "-cert", cert)...)
		return reply, err
	case "probe":
		if a.Role != "edge" {
			break
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:18889/", nil)
		if err != nil {
			return reply, err
		}
		req.Host = "preview.octaviosantacruz.com"
		transport := &http.Transport{MaxResponseHeaderBytes: 64 << 10}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(req)
		if err != nil {
			return reply, errors.New("pilot HTTP probe failed")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return reply, errors.New("pilot HTTP probe did not return 200")
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, io.LimitReader(response.Body, 8<<20)); err != nil {
			return reply, err
		}
		reply.Digest = hex.EncodeToString(hash.Sum(nil))
		return reply, nil
	}
	return reply, errors.New("unsupported host action")
}

type Pilot struct{ Home string }

// PilotEdgeAgent is the command the pilot's controller runs on the edge over
// SSH (postura 2, 2026-10-22): the edge binary as the tunnel's service
// account, with fixed arguments. The edge's sudoers rule names exactly this
// command, so octavio's shell can run the agent and nothing else as that
// account.
const PilotEdgeAgent = "sudo -n -u switchboard-tunnel /usr/local/lib/switchboard-tunnel-edge/switchboard tunnel renew-agent -role edge -dir /var/lib/switchboard-tunnel-edge -config /etc/switchboard-tunnel-edge/config.integrated.json -unit switchboard-tunnel-edge"

func (p Pilot) Call(ctx context.Context, role string, request Request) (Reply, error) {
	if role == "connector" {
		return (Agent{Home: p.Home, Role: role}).Handle(ctx, request)
	}
	if role != "edge" {
		return Reply{}, errors.New("invalid role")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return Reply{}, err
	}
	output, err := command(ctx, data, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", "-o", "StrictHostKeyChecking=yes", "-o", "IdentitiesOnly=yes", "-i", filepath.Join(p.Home, ".ssh/id_ed25519_hetzner_homelab"), "octavio@10.8.0.1", PilotEdgeAgent)
	if err != nil {
		return Reply{}, err
	}
	var reply Reply
	if err := json.Unmarshal(output, &reply); err != nil {
		return Reply{}, errors.New("invalid renewal agent response")
	}
	return reply, nil
}

func (p Pilot) Healthy(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	for {
		data, err := os.ReadFile(filepath.Join(p.Home, "projects/portfolio/dist/index.html"))
		if err != nil {
			return err
		}
		expected := sha256.Sum256(data)
		reply, err := p.Call(ctx, "edge", Request{Action: "probe"})
		if err == nil && reply.Digest == hex.EncodeToString(expected[:]) {
			return nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.New("renewed tunnel did not become healthy")
		case <-timer.C:
		}
	}
}
