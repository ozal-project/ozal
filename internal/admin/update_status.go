package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const maxUpdateStatusBytes = 64 << 10

var updateVersionText = regexp.MustCompile(`^[A-Za-z0-9.+-]{1,64}$`)
var updateIdentifierText = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// WithUpdateStatusFile configures the fixed local status source. An invalid
// path disables status reads and reports; requests cannot choose another path.
func WithUpdateStatusFile(path string) Option {
	return func(h *Handler) {
		if filepath.IsAbs(path) && filepath.Clean(path) == path {
			h.updateStatusFile = path
		}
	}
}

// updateStatusView is the secret-free projection shared by the updater and
// the authenticated console API. A pair job may include its two participants.
type updateStatusView struct {
	CheckedAt string             `json:"checked_at"`
	Installed string             `json:"installed"`
	Latest    string             `json:"latest,omitempty"`
	Result    string             `json:"result"`
	Reason    string             `json:"reason,omitempty"`
	JobID     string             `json:"job_id,omitempty"`
	Phase     string             `json:"phase,omitempty"`
	Nodes     []updateNodeStatus `json:"nodes,omitempty"`
}

type updateNodeStatus struct {
	Role      string `json:"role"`
	Name      string `json:"name"`
	Installed string `json:"installed,omitempty"`
	Latest    string `json:"latest,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Result    string `json:"result"`
}

// updateStatus serves only the fixed local status file. It never runs a host
// command and does not accept paths or selectors from the request.
func (h *Handler) updateStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.updateStatusFile == "" {
		writeError(w, http.StatusServiceUnavailable, "update status is unavailable")
		return
	}
	info, err := os.Lstat(h.updateStatusFile)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusOK, updateStatusView{Result: "unknown", Reason: publicUpdateReason("unknown")})
		return
	}
	if err != nil || info == nil || !info.Mode().IsRegular() || info.Size() > maxUpdateStatusBytes {
		writeError(w, http.StatusServiceUnavailable, "update status is unavailable")
		return
	}
	raw, err := os.ReadFile(h.updateStatusFile)
	if err != nil || len(raw) > maxUpdateStatusBytes {
		writeError(w, http.StatusServiceUnavailable, "update status is unavailable")
		return
	}
	var status updateStatusView
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil {
		writeError(w, http.StatusServiceUnavailable, "update status is unavailable")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) || validateUpdateStatus(status) != nil {
		writeError(w, http.StatusServiceUnavailable, "update status is unavailable")
		return
	}
	if status.Result == "" {
		status.Result = "unknown"
	}
	status.Reason = publicUpdateReason(status.Result)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, status)
}

// reportUpdateStatus accepts a small status record only over the host-only
// Unix socket. It cannot start updates or choose any host command or path.
func (h *Handler) reportUpdateStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.updateStatusFile == "" {
		writeError(w, http.StatusServiceUnavailable, "update status is unavailable")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxUpdateStatusBytes+1))
	if err != nil || len(raw) > maxUpdateStatusBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "update status payload is too large")
		return
	}
	var status updateStatusView
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil {
		writeError(w, http.StatusBadRequest, "invalid update status")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid update status")
		return
	}
	if err := validateUpdateStatus(status); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status.Reason = publicUpdateReason(status.Result)
	if err := saveUpdateStatus(h.updateStatusFile, status); err != nil {
		writeError(w, http.StatusServiceUnavailable, "update status could not be saved")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, status)
}

func validateUpdateStatus(status updateStatusView) error {
	if !validUpdateResult(status.Result) || status.Result == "" {
		return errors.New("result is not a supported update status")
	}
	if _, err := time.Parse(time.RFC3339, status.CheckedAt); err != nil {
		return errors.New("checked_at must be RFC3339")
	}
	if !safeUpdateVersion(status.Installed) || !safeUpdateVersion(status.Latest) {
		return errors.New("installed and latest must be short version strings")
	}
	if status.JobID != "" && !updateIdentifierText.MatchString(status.JobID) {
		return errors.New("job_id is invalid")
	}
	if status.Phase != "" && !validUpdatePhase(status.Phase) {
		return errors.New("phase is not a supported update phase")
	}
	if len(status.Nodes) > 2 {
		return errors.New("at most two update nodes are supported")
	}
	if status.JobID != "" && (status.Phase == "" || len(status.Nodes) != 2) {
		return errors.New("a paired update needs a phase and both nodes")
	}
	if status.JobID == "" && len(status.Nodes) != 0 {
		return errors.New("update nodes require a paired job id")
	}
	seen := map[string]bool{}
	for _, node := range status.Nodes {
		if (node.Role != "edge" && node.Role != "connector") || !updateIdentifierText.MatchString(node.Name) {
			return errors.New("update node identity is invalid")
		}
		if seen[node.Role] {
			return errors.New("update node roles must be unique")
		}
		seen[node.Role] = true
		if !safeUpdateVersion(node.Installed) || !safeUpdateVersion(node.Latest) || !validNodeUpdateResult(node.Result) {
			return errors.New("update node version or result is invalid")
		}
		if node.SHA256 != "" && !validUpdateSHA256(node.SHA256) {
			return errors.New("update node sha256 is invalid")
		}
	}
	if status.JobID != "" && (!seen["edge"] || !seen["connector"]) {
		return errors.New("a paired update needs one edge and one connector")
	}
	return nil
}

func saveUpdateStatus(path string, status updateStatusView) error {
	raw, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-status-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func safeUpdateVersion(value string) bool { return value == "" || updateVersionText.MatchString(value) }

func publicUpdateReason(result string) string {
	switch result {
	case "unknown":
		return "no update run has reported status yet"
	case "current":
		return "this host is on the newest accepted release"
	case "unversioned":
		return "the installed build is not a versioned release"
	case "available":
		return "a signed release is available"
	case "updating":
		return "the coordinated update is in progress"
	case "updated":
		return "the update completed"
	case "rolled_back":
		return "the previous versions were restored"
	case "intervention_required":
		return "the update failed and needs operator intervention"
	case "failed":
		return "the update failed; inspect the host update log for details"
	default:
		return "update status is unknown"
	}
}

func validUpdateResult(result string) bool {
	switch result {
	case "", "unknown", "current", "unversioned", "available", "updating", "updated", "rolled_back", "intervention_required", "failed":
		return true
	default:
		return false
	}
}

func validUpdatePhase(value string) bool {
	switch value {
	case "preflight", "staging", "checkpointing", "stopping", "installing", "starting", "verifying", "rolling_back", "complete":
		return true
	default:
		return false
	}
}

func validNodeUpdateResult(value string) bool {
	switch value {
	case "pending", "staged", "updated", "rolled_back", "failed":
		return true
	default:
		return false
	}
}

func validUpdateSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
