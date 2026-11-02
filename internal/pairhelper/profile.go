// Package pairhelper implements the privileged half of a coordinated edge
// update. Its input is an action request, never a command line or shell script.
package pairhelper

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ozal-project/ozal/internal/ctl"
	"github.com/ozal-project/ozal/internal/release"
)

const ProfilePath = "/etc/ozal/pair-helper.json"

var accountName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
var groupName = regexp.MustCompile(`^(?:[a-z_][a-z0-9_-]{0,31}|[0-9]{1,5})$`)
var unitName = regexp.MustCompile(`^[a-zA-Z0-9_.@-]{1,128}$`)
var jobName = regexp.MustCompile(`^pair-[0-9]{8}T[0-9]{6}Z-[a-f0-9]{16}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Profile struct {
	Unit         string `json:"unit"`
	ServiceUser  string `json:"service_user"`
	ServiceGroup string `json:"service_group"`
	Binary       string `json:"binary"`
	Config       string `json:"config"`
	StateDB      string `json:"state_db"`
	Stage        string `json:"stage"`
	Socket       string `json:"socket"`
	Peer         string `json:"peer"`
	Keep         int    `json:"keep"`
}

type Request struct {
	Action   string          `json:"action"`
	Job      string          `json:"job,omitempty"`
	SHA256   string          `json:"sha256,omitempty"`
	Ref      string          `json:"ref,omitempty"`
	Progress json.RawMessage `json:"progress,omitempty"`
}

func DecodeProfile(r io.Reader) (Profile, error) {
	var p Profile
	d := json.NewDecoder(io.LimitReader(r, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return Profile{}, fmt.Errorf("decode pair helper profile: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Profile{}, errors.New("pair helper profile has trailing data")
	}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func DecodeRequest(r io.Reader) (Request, error) {
	var q Request
	d := json.NewDecoder(io.LimitReader(r, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(&q); err != nil {
		return Request{}, fmt.Errorf("decode pair helper request: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Request{}, errors.New("pair helper request has trailing data")
	}
	if err := q.Validate(); err != nil {
		return Request{}, err
	}
	return q, nil
}

func (p Profile) Validate() error {
	if !unitName.MatchString(p.Unit) || !accountName.MatchString(p.ServiceUser) ||
		!groupName.MatchString(p.ServiceGroup) || !absoluteClean(p.Binary) ||
		!absoluteClean(p.Config) || !absoluteClean(p.StateDB) ||
		!absoluteClean(p.Stage) || !absoluteClean(p.Socket) ||
		!accountName.MatchString(p.Peer) {
		return errors.New("pair helper profile contains an invalid unit, identity, path, or peer")
	}
	if p.Keep < 1 || p.Keep > 20 {
		return errors.New("pair helper profile keep count must be between 1 and 20")
	}
	if filepath.Dir(p.Binary) == p.Stage || strings.HasPrefix(p.Stage+string(filepath.Separator), filepath.Dir(p.Binary)+string(filepath.Separator)) {
		return errors.New("pair helper staging directory must be outside the installed binary directory")
	}
	return nil
}

func (q Request) Validate() error {
	switch q.Action {
	case "preflight", "start", "stop":
		if q.Job != "" || q.SHA256 != "" || q.Ref != "" || len(q.Progress) != 0 {
			return errors.New("action does not accept job, hash, ref, or progress fields")
		}
	case "checkpoint", "install", "ready", "restore":
		if !jobName.MatchString(q.Job) {
			return errors.New("action requires a valid pair job identifier")
		}
		if (q.Action == "install" || q.Action == "ready" || q.Action == "checkpoint") && !hashPattern.MatchString(q.SHA256) {
			return errors.New("action requires a valid SHA-256")
		}
		if q.Action == "install" && !release.IsVersion(q.Ref) {
			return errors.New("install action requires a semantic release tag")
		}
		if q.Action != "install" && q.Ref != "" {
			return errors.New("action does not accept a release tag")
		}
		if q.Action == "restore" && q.SHA256 != "" {
			return errors.New("action does not accept a SHA-256")
		}
		if len(q.Progress) != 0 {
			return errors.New("action does not accept progress")
		}
	case "report":
		if len(q.Progress) == 0 || len(q.Progress) > 32<<10 || q.Job != "" || q.SHA256 != "" || q.Ref != "" {
			return errors.New("report requires bounded progress JSON only")
		}
		var progress ctl.PairProgress
		if err := json.Unmarshal(q.Progress, &progress); err != nil {
			return errors.New("report progress is not a valid pair status object")
		}
		switch progress.Phase {
		case "staging", "checkpointing", "stopping", "starting", "verifying", "rolling_back", "complete":
		default:
			return errors.New("report progress has an invalid phase")
		}
		switch progress.Result {
		case "current", "updating", "updated", "rolled_back", "intervention_required":
		default:
			return errors.New("report progress has an invalid result")
		}
	default:
		return errors.New("unknown pair helper action")
	}
	return nil
}

// ValidateUpgrade keeps a key holder from selecting an older, still-signed
// release to downgrade the root-owned edge executable.
func ValidateUpgrade(installedVersion, installedHash, candidateVersion, candidateHash string) error {
	if !release.IsVersion(installedVersion) {
		return errors.New("refusing helper installation over an unversioned edge build")
	}
	if !release.IsVersion(candidateVersion) || !hashPattern.MatchString(candidateHash) || !hashPattern.MatchString(installedHash) {
		return errors.New("candidate release identity is invalid")
	}
	if comparison := release.Compare(candidateVersion, installedVersion); comparison < 0 {
		return errors.New("refusing to install an older signed release")
	} else if comparison == 0 {
		if candidateHash != installedHash {
			return errors.New("refusing a different binary for an installed release tag")
		}
		return errors.New("candidate release is already installed")
	}
	return nil
}

func absoluteClean(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsRune(value, '\x00')
}
