package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ozal-project/ozal/internal/ctl/artifact"
	"github.com/ozal-project/ozal/internal/pages"
)

type Acceptance struct {
	URL, HTTPURL string
	Proof        artifact.Proof
	Client       *http.Client
	Attempts     int
	Wait         time.Duration
}

func publicURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("public URL must be an http(s) origin without credentials, query or path")
	}
	return u, nil
}

func withoutRedirects(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &clone
}

func publicGet(ctx context.Context, client *http.Client, address string) (int, []byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, nil, "", errors.New("invalid public check URL")
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, "", errors.New("public request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, pages.DefaultLimits.MaxFileBytes+1))
	if err != nil || int64(len(body)) > pages.DefaultLimits.MaxFileBytes {
		return response.StatusCode, nil, "", errors.New("public response is unreadable or exceeds the file limit")
	}
	return response.StatusCode, body, response.Header.Get("Location"), nil
}

// Accept verifies stable file bytes instead of HTML that a proxy may rewrite.
// It does not follow redirects to another host or accept SPA fallback as 404.
func Accept(ctx context.Context, out io.Writer, r Acceptance) error {
	u, err := publicURL(r.URL)
	if err != nil {
		return err
	}
	if r.Proof.Path == "" || len(r.Proof.SHA256) != 64 {
		return errors.New("a public asset proof is required")
	}
	if _, err := hex.DecodeString(r.Proof.SHA256); err != nil {
		return errors.New("invalid public asset digest")
	}
	decision := pages.CheckEntry(r.Proof.Path, pages.KindFile, 0, pages.DefaultLimits)
	if !decision.Allowed || decision.Path == "" {
		return errors.New("invalid public asset path")
	}
	client := withoutRedirects(r.Client)
	base := strings.TrimRight(u.String(), "/")
	asset := *u
	asset.Path = "/" + decision.Path
	if decision.Path == "index.html" {
		asset.Path = "/"
	}
	attempts, wait := r.Attempts, r.Wait
	if attempts <= 0 {
		attempts = 10
	}
	if wait <= 0 {
		wait = 3 * time.Second
	}
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		status, _, _, e := publicGet(ctx, client, base+"/")
		if e != nil || status != 200 {
			last = errors.New("public home did not answer 200")
			continue
		}
		status, body, _, e := publicGet(ctx, client, asset.String())
		sum := sha256.Sum256(body)
		if e != nil || status != 200 || hex.EncodeToString(sum[:]) != r.Proof.SHA256 {
			last = errors.New("public asset does not match the reviewed build")
			continue
		}
		last = nil
		break
	}
	if last != nil {
		return last
	}
	fmt.Fprintln(out, "Public home and asset match the build.")
	for _, path := range []string{"/_headers", "/.env", "/no-such-file-deploy-check.js", "/no-such-page-deploy-check"} {
		status, _, _, e := publicGet(ctx, client, base+path)
		if e != nil || status != 404 {
			return fmt.Errorf("public refusal check %s did not answer 404", path)
		}
	}
	if u.Scheme == "https" {
		plain := r.HTTPURL
		if plain == "" {
			httpURL := *u
			httpURL.Scheme = "http"
			plain = httpURL.String()
		}
		if _, err := publicURL(plain); err != nil {
			return err
		}
		status, _, location, e := publicGet(ctx, client, strings.TrimRight(plain, "/")+"/")
		target, parseErr := url.Parse(location)
		if e != nil || parseErr != nil || (status != 301 && status != 308) || target.Scheme != "https" || !strings.EqualFold(target.Host, u.Host) {
			return errors.New("plain HTTP did not redirect to this HTTPS origin")
		}
	}
	fmt.Fprintln(out, "Public paths and HTTP redirect checks passed.")
	return nil
}
