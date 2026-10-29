package publish

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/ozal-project/ozal/internal/ctl/artifact"
	"github.com/ozal-project/ozal/internal/pages"
)

type TokenSource interface {
	Token(context.Context, string) (string, error)
}

// GitHubTokenSource exchanges the runner credential for an audience-bound
// OIDC token. Neither credential is printed, returned in errors or persisted.
type GitHubTokenSource struct {
	URL, Authorization string
	Client             *http.Client
}

func (s GitHubTokenSource) Token(ctx context.Context, audience string) (string, error) {
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || s.Authorization == "" {
		return "", errors.New("GitHub Actions OIDC environment is missing or invalid")
	}
	query := u.Query()
	query.Set("audience", audience)
	u.RawQuery = query.Encode()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", errors.New("cannot prepare GitHub OIDC request")
	}
	r.Header.Set("Authorization", "Bearer "+s.Authorization)
	response, err := withoutRedirects(s.Client).Do(r)
	if err != nil {
		return "", errors.New("GitHub OIDC request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", errors.New("GitHub OIDC request was refused")
	}
	var body struct {
		Value string `json:"value"`
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 32<<10))
	if err != nil || json.Unmarshal(data, &body) != nil || body.Value == "" || len(body.Value) > 16<<10 || strings.IndexFunc(body.Value, unicode.IsSpace) >= 0 {
		return "", errors.New("GitHub did not return a valid OIDC token")
	}
	return body.Value, nil
}

type Receipt struct {
	Project   string `json:"project"`
	ID        string `json:"id"`
	Live      bool   `json:"live"`
	CommitSHA string `json:"commit_sha,omitempty"`
}

type CIRequest struct {
	Endpoint, Project, ResultFile string
	Archive                       []byte
	Apply                         bool
	Client                        *http.Client
	// Confirmed is called only after a validated 201/live response. Actions
	// may use it to enable rollback even if the later status check fails.
	Confirmed func(Receipt) error
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func CI(ctx context.Context, out io.Writer, issuer TokenSource, r CIRequest) (Receipt, error) {
	endpoint, err := publicURL(r.Endpoint)
	if err != nil || endpoint.Scheme != "https" || !pages.ValidSiteName(r.Project) {
		return Receipt{}, errors.New("HTTPS endpoint and valid project are required")
	}
	// Check the exact upload bytes before requesting any credential.
	snapshot, err := artifact.FromArchive(r.Archive)
	if err != nil {
		return Receipt{}, err
	}
	if !r.Apply {
		fmt.Fprintf(out, "Plan: publish %d verified bytes to project %s; no token requested.\n", len(snapshot.Archive), r.Project)
		return Receipt{}, nil
	}
	if issuer == nil || r.ResultFile == "" {
		return Receipt{}, errors.New("token source and a new result file are required")
	}
	result, err := os.OpenFile(r.ResultFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Receipt{}, errors.New("result must be a new private writable file; nothing uploaded")
	}
	defer result.Close()
	if err := json.NewEncoder(result).Encode(map[string]any{"project": r.Project, "confirmed": false}); err != nil {
		return Receipt{}, errors.New("cannot reserve deployment receipt; nothing uploaded")
	}
	if err := result.Sync(); err != nil {
		return Receipt{}, errors.New("cannot sync deployment receipt; nothing uploaded")
	}
	token, err := issuer.Token(ctx, r.Endpoint)
	if err != nil {
		return Receipt{}, err
	}
	if token == "" || len(token) > 16<<10 || strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		return Receipt{}, errors.New("token source returned an invalid credential")
	}
	client := withoutRedirects(r.Client)
	base := strings.TrimRight(r.Endpoint, "/") + "/v1/projects/" + url.PathEscape(r.Project) + "/deployments"
	call := func(method, address string, body io.Reader, expected int) (Receipt, error) {
		deadline, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(deadline, method, address, body)
		if err != nil {
			return Receipt{}, errors.New("cannot prepare deployment request")
		}
		request.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			request.Header.Set("Content-Type", "application/gzip")
		}
		response, err := client.Do(request)
		if err != nil {
			return Receipt{}, errors.New("deployment request was not confirmed; reconcile before retrying")
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if err != nil || len(data) > 1<<20 {
			return Receipt{}, errors.New("deployment response was not readable; reconcile before retrying")
		}
		if response.StatusCode != expected {
			return Receipt{}, fmt.Errorf("deployment request refused: HTTP %d (response content suppressed)", response.StatusCode)
		}
		var receipt Receipt
		if json.Unmarshal(data, &receipt) != nil || !validHex(receipt.ID, 64) || !receipt.Live || receipt.Project != r.Project || (receipt.CommitSHA != "" && !validHex(receipt.CommitSHA, 40)) {
			return Receipt{}, errors.New("deployment response did not confirm this project/id/live state; reconcile before retrying")
		}
		return receipt, nil
	}
	receipt, err := call(http.MethodPost, base, bytes.NewReader(snapshot.Archive), 201)
	if err != nil {
		return Receipt{}, err
	}
	if r.Confirmed != nil {
		if err := r.Confirmed(receipt); err != nil {
			return receipt, errors.New("deployment is live but workflow output failed; reconcile before retrying")
		}
	}
	if _, err := result.Seek(0, io.SeekStart); err != nil {
		return receipt, errors.New("deployment is live but receipt update failed")
	}
	if err := result.Truncate(0); err != nil {
		return receipt, errors.New("deployment is live but receipt update failed")
	}
	if err := json.NewEncoder(result).Encode(receipt); err != nil {
		return receipt, errors.New("deployment is live but receipt update failed")
	}
	if err := result.Sync(); err != nil {
		return receipt, errors.New("deployment is live but receipt sync failed")
	}
	status, err := call(http.MethodGet, base+"/"+receipt.ID, nil, 200)
	if err != nil {
		return receipt, err
	}
	if status.ID != receipt.ID {
		return receipt, errors.New("deployment status returned another deployment")
	}
	fmt.Fprintf(out, "Deployment %s is live and its authenticated status is confirmed.\n", receipt.ID[:12])
	return receipt, nil
}
