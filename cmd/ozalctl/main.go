// Command ozalctl is the operator's tool for shipping Ozal to edges. It
// runs on the operator's machine and talks to servers over SSH; it is never
// installed on a server, and the edge binary does not contain it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ozal-project/ozal/internal/certs"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/ctl"
	"github.com/ozal-project/ozal/internal/ctl/publish"
	"github.com/ozal-project/ozal/internal/ctl/sshexec"
)

const usage = `usage:
  ozalctl api [-targets FILE] [-target NAME] [-d JSON|@FILE|@-] METHOD /_/api/PATH
      Call the edge's admin API through its socket over SSH, as your SSH user
      (edge.socket_group): no sudo and no token on this machine. Prints the
      answer; an HTTP error exits 1. Example: ozalctl api GET /_/api/zones
  ozalctl site check -dir DIST
  ozalctl site pack -dir DIST -out ARCHIVE
  ozalctl site deploy -endpoint HTTPS_URL -project NAME -archive ARCHIVE -result FILE [-apply]
      CI upload with GitHub Actions OIDC; credentials stay in memory.
  ozalctl site accept -url HTTPS_URL -dir DIST [-http-url HTTP_URL]
      Check public content hashes, hidden paths and the HTTPS redirect.
  ozalctl cloudflare origin-pull -host HOST -like SOURCE -token-file FILE [-backup FILE] [-apply]
  ozalctl cloudflare deploy-rule -host HOST -token-file FILE [-backup FILE] [-apply]
      Plan a scoped optional provider operation; back up metadata before apply.
  ozalctl project trust -project NAME -repository-id ID -owner-id ID -workflow-ref WORKFLOW [-ref REF] [-apply]
      Migrate an existing GitHub publisher through the edge's private socket.
      The default is a plan; -apply keeps previous trust metadata on the edge.
  ozalctl edge upgrade [-targets FILE] [-target NAME] [-repo DIR] [-ref REF] [-apply]
      Build a committed ref, upload it and run "ozal upgrade" on the edge.
  ozalctl edge pair-upgrade [-targets FILE] [-target NAME] -ref TAG -public-key FILE [-api URL] [-force] [-apply]
      Verify one signed release and coordinate the configured tunnel edge and connector.
  ozalctl edge config -base FILE [-cloudflare-ranges] [-targets FILE] [-target NAME] [-apply]
      Install the host settings from FILE (listeners, TLS, identity, proxies,
      DNS). Routes and projects stay the edge's: the first install creates its
      runtime file from the routes in effect. -cloudflare-ranges refreshes
      trusted_proxies from Cloudflare's published lists.
  ozalctl site publish -site NAME -dir DIST [-host HOST] [-targets FILE] [-target NAME] [-apply]
      Upload a built directory as the site's live build through an SSH tunnel
      with the sites token; with -host, check the public answer and roll back
      on failure. Add the address itself once in the console (Add website).

  ozalctl app alias -from HOST -to HOST [-manual-dns] [-wait 90s] [-targets FILE] [-target NAME] [-apply]
      Serve an existing public site at a second hostname: the edge saves the
      route with the reviewed digest, creates the proxied DNS record with its
      own provider credential (dns.cloudflare) and waits until dns,
      certificate and reach are ok. The previous hostname is never changed and
      an existing record is never replaced. Runs "ozal app alias" on the edge
      against its admin socket: as your SSH user when the target sets
      edge.socket_group, otherwise as the service account through sudo (your
      terminal).

  ozalctl site cutover -host HOST -to IP -cloudflare-token FILE [-dir DIST] [-apply]
  ozalctl site cutover -host HOST -back EVIDENCE -cloudflare-token FILE [-apply]
      Move a live hostname (for example an apex served by Cloudflare Pages) to
      the edge: its address records are replaced by a proxied A record in one
      DNS batch, the edge must answer (and serve DIST's index.html), and any
      failure or Ctrl-C puts the previous record back. The replaced records
      are kept under ~/.local/state/ozalctl; -back restores them.

Without -apply every command only prints the plan. The targets file lists one
entry per server (see ozalctl.example.json).
`

// Cloudflare's published proxy ranges.
var cloudflareLists = []string{"https://www.cloudflare.com/ips-v4", "https://www.cloudflare.com/ips-v6"}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 && args[0] == "api" {
		return apiCall(args[1:])
	}
	if len(args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	commands := map[string]func([]string) int{
		"edge upgrade":           edgeUpgrade,
		"edge pair-upgrade":      edgePairUpgrade,
		"edge config":            edgeConfig,
		"site publish":           sitePublish,
		"app alias":              appAlias,
		"project trust":          projectTrust,
		"site check":             func(args []string) int { return siteSnapshot(args, false) },
		"site pack":              func(args []string) int { return siteSnapshot(args, true) },
		"site deploy":            siteDeploy,
		"site accept":            siteAccept,
		"cloudflare origin-pull": func(args []string) int { return cloudflareOperation(args, "origin-pull") },
		"cloudflare deploy-rule": func(args []string) int { return cloudflareOperation(args, "deploy-rule") },
		"site cutover":           siteCutover,
	}
	command, ok := commands[args[0]+" "+args[1]]
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	return command(args[2:])
}

// common holds what every command needs: the target and the run's owner.
type common struct {
	name   string
	target ctl.Target
	owner  string
	remote sshexec.Remote
}

func setup(fs *flag.FlagSet, args []string) (*common, bool) {
	home, _ := os.UserHomeDir()
	targetsPath := fs.String("targets", defaultTargets(home), "targets file")
	name := fs.String("target", "", "target name (optional when the file has one)")
	if err := fs.Parse(args); err != nil {
		return nil, false
	}
	file, err := os.Open(*targetsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ozalctl:", err)
		return nil, false
	}
	targets, err := ctl.ParseTargets(file)
	file.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ozalctl:", err)
		return nil, false
	}
	targetName, target, err := targets.Pick(*name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ozalctl:", err)
		return nil, false
	}
	host, _ := os.Hostname()
	return &common{
		name: targetName, target: target,
		owner:  fmt.Sprintf("ozalctl on %s pid %d since %s", host, os.Getpid(), time.Now().UTC().Format(time.RFC3339)),
		remote: sshexec.Remote{Target: target.SSH.Target, Key: target.SSH.Key, Interactive: isTerminal(os.Stdin)},
	}, true
}

func finish(err error) int {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ozalctl:", err)
		return 1
	}
	return 0
}

func edgeUpgrade(args []string) int {
	fs := flag.NewFlagSet("edge upgrade", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Ozal checkout to build from")
	ref := fs.String("ref", "HEAD", "committed ref to build")
	apply := fs.Bool("apply", false, "do it (default: print the plan)")
	c, ok := setup(fs, args)
	if !ok {
		return 2
	}
	if c.target.Connector != nil {
		return finish(errors.New("this target has a tunnel connector; use `ozalctl edge pair-upgrade` so both peers move together"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	home, _ := os.UserHomeDir()
	builder := sshexec.GitBuilder{Repo: *repo, GOOS: "linux", GOARCH: "amd64", WorkDir: filepath.Join(home, ".cache", "ozalctl")}
	req := ctl.UpgradeRequest{Name: c.name, Target: c.target, Ref: *ref, Apply: *apply, Owner: c.owner}
	return finish(ctl.Upgrade(ctx, os.Stdout, c.remote, builder, req))
}

func edgeConfig(args []string) int {
	fs := flag.NewFlagSet("edge config", flag.ContinueOnError)
	basePath := fs.String("base", "", "the edge's host settings (JSON, no routes)")
	refresh := fs.Bool("cloudflare-ranges", false, "replace trusted_proxies with Cloudflare's published ranges")
	apply := fs.Bool("apply", false, "do it (default: print the plan)")
	c, ok := setup(fs, args)
	if !ok {
		return 2
	}
	if *basePath == "" {
		fmt.Fprintln(os.Stderr, "ozalctl: -base is required")
		return 2
	}
	base, err := os.ReadFile(*basePath)
	if err != nil {
		return finish(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var ranges []string
	if *refresh {
		if ranges, err = fetchRanges(ctx); err != nil {
			return finish(err)
		}
	}
	req := ctl.ConfigRequest{Name: c.name, Target: c.target, Base: base, Ranges: ranges, Apply: *apply, Owner: c.owner,
		Stamp: time.Now().UTC().Format("20060102T150405Z")}
	return finish(ctl.ApplyConfig(ctx, os.Stdout, c.remote, req))
}

func fetchRanges(ctx context.Context) ([]string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	var lists []string
	for _, u := range cloudflareLists {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: status %d %v", u, res.StatusCode, err)
		}
		lists = append(lists, string(body))
	}
	return ctl.ParseRanges(lists...)
}

func sitePublish(args []string) int {
	fs := flag.NewFlagSet("site publish", flag.ContinueOnError)
	site := fs.String("site", "", "site name")
	dir := fs.String("dir", "", "built directory to publish (for example dist)")
	host := fs.String("host", "", "public address to check after publishing")
	apply := fs.Bool("apply", false, "do it (default: print the plan)")
	c, ok := setup(fs, args)
	if !ok {
		return 2
	}
	if *site == "" || *dir == "" {
		fmt.Fprintln(os.Stderr, "ozalctl: -site and -dir are required")
		return 2
	}
	token := ""
	if *apply {
		if c.target.Edge.SitesToken == "" {
			return finish(errors.New("edge.sites_token is required in the targets file"))
		}
		var err error
		if token, err = sshexec.ReadToken(c.target.Edge.SitesToken); err != nil {
			return finish(err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req := publish.Request{Target: c.target, Site: *site, Dir: *dir, Host: *host, Token: token, Apply: *apply,
		Admin:  &http.Client{Timeout: 10 * time.Minute},
		Public: &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect}}
	return finish(publish.Run(ctx, os.Stdout, c.remote, req))
}

const pagesHint = "if the name is a custom domain of a Cloudflare Pages project, Cloudflare keeps sending it to Pages whatever the DNS says: remove the custom domain in Pages (Workers & Pages → project → Custom domains) now"

func apiCall(args []string) int {
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
	data := fs.String("d", "", "JSON body, @FILE to read it from a file or @- from stdin")
	c, ok := setup(fs, args)
	if !ok {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: ozalctl api [-target NAME] [-d JSON|@FILE|@-] METHOD /_/api/PATH")
		return 2
	}
	body, err := apiBody(*data)
	if err != nil {
		return finish(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return finish(ctl.API(ctx, os.Stdout, c.remote, ctl.APIRequest{Name: c.name, Target: c.target,
		Method: fs.Arg(0), Path: fs.Arg(1), Body: body}))
}

// apiBody reads -d like curl: inline, @FILE or @- for stdin.
func apiBody(value string) ([]byte, error) {
	switch {
	case value == "":
		return nil, nil
	case value == "@-":
		return io.ReadAll(io.LimitReader(os.Stdin, 1<<20+1))
	case strings.HasPrefix(value, "@"):
		return os.ReadFile(strings.TrimPrefix(value, "@"))
	default:
		return []byte(value), nil
	}
}

func appAlias(args []string) int {
	fs := flag.NewFlagSet("app alias", flag.ContinueOnError)
	from := fs.String("from", "", "hostname of the existing public site")
	to := fs.String("to", "", "new hostname that serves the same site")
	manual := fs.Bool("manual-dns", false, "save the route and check it, leaving the DNS record to you")
	wait := fs.Duration("wait", 90*time.Second, "how long the edge waits for the hostname to connect")
	apply := fs.Bool("apply", false, "do it (default: print the plan)")
	c, ok := setup(fs, args)
	if !ok {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return finish(ctl.Alias(ctx, os.Stdout, c.remote, ctl.AliasRequest{Name: c.name, Target: c.target,
		From: *from, To: *to, ManualDNS: *manual, Wait: *wait, Apply: *apply}))
}

func siteCutover(args []string) int {
	fs := flag.NewFlagSet("site cutover", flag.ContinueOnError)
	host := fs.String("host", "", "hostname to move")
	to := fs.String("to", "", "the edge's public IPv4 address")
	back := fs.String("back", "", "evidence file of an earlier cutover to restore")
	tokenPath := fs.String("cloudflare-token", "", "file with a Cloudflare token limited to Zone DNS Edit")
	dir := fs.String("dir", "", "built directory whose index.html the edge must serve")
	apply := fs.Bool("apply", false, "do it (default: print the plan)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *host == "" || *tokenPath == "" || (*to == "") == (*back == "") {
		fmt.Fprintln(os.Stderr, "ozalctl: -host, -cloudflare-token and exactly one of -to or -back are required")
		return 2
	}
	token, err := sshexec.ReadToken(*tokenPath)
	if err != nil {
		return finish(err)
	}
	client, err := certs.NewCloudflareClient(certs.CloudflareConfig{Token: token})
	if err != nil {
		return finish(err)
	}
	record := certs.SiteRecord{Type: "A", Name: *host, Content: *to, Proxied: true}
	if *back != "" {
		if record, err = publish.SavedRecord(*back); err != nil {
			return finish(err)
		}
	} else if addr, perr := netip.ParseAddr(*to); perr != nil || !addr.Is4() {
		return finish(errors.New("-to must be the edge's public IPv4 address"))
	}
	var index []byte
	if *dir != "" {
		if index, err = os.ReadFile(filepath.Join(*dir, "index.html")); err != nil {
			return finish(err)
		}
	}
	home, _ := os.UserHomeDir()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req := publish.CutoverRequest{Host: *host, To: record, Back: *back != "", Apply: *apply, Index: index, Hint: pagesHint,
		Evidence: filepath.Join(home, ".local", "state", "ozalctl"), Stamp: time.Now().UTC().Format("20060102T150405Z"),
		Public: &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect}}
	return finish(publish.Cutover(ctx, os.Stdout, client, req))
}

func projectTrust(args []string) int {
	fs := flag.NewFlagSet("project trust", flag.ContinueOnError)
	project := fs.String("project", "", "existing project")
	trust := config.ProjectGitHubTrust{}
	fs.Int64Var(&trust.RepositoryID, "repository-id", 0, "verified numeric GitHub repository ID")
	fs.Int64Var(&trust.RepositoryOwnerID, "owner-id", 0, "verified numeric GitHub owner ID")
	fs.StringVar(&trust.Ref, "ref", "refs/heads/main", "only ref allowed to deploy")
	fs.StringVar(&trust.WorkflowRef, "workflow-ref", "", "exact GitHub workflow_ref claim")
	apply := fs.Bool("apply", false, "save the reviewed trust (default: plan)")
	c, ok := setup(fs, args)
	if !ok {
		return 2
	}
	if fs.NArg() != 0 {
		return finish(errors.New("unexpected project trust arguments"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return finish(ctl.ProjectTrust(ctx, os.Stdout, c.remote, ctl.ProjectTrustRequest{
		Name: c.name, Target: c.target, Project: *project, Trust: trust, Apply: *apply,
		Stamp: time.Now().UTC().Format("20060102T150405.000000000Z"),
	}))
}

func defaultTargets(home string) string {
	if _, err := os.Stat("ozalctl.json"); err == nil {
		return "ozalctl.json"
	}
	current := filepath.Join(home, ".config", "ozalctl", "targets.json")
	if _, err := os.Stat(current); err != nil {
		// Read the file the tool had before its rename; nothing is moved.
		if old := filepath.Join(home, ".config", "sbctl", "targets.json"); fileExists(old) {
			return old
		}
	}
	return current
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
