// Command sbctl is the operator's tool for shipping Switchboard to edges. It
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
	"syscall"
	"time"

	"github.com/switchboard-project/switchboard/internal/certs"
	"github.com/switchboard-project/switchboard/internal/ctl"
	"github.com/switchboard-project/switchboard/internal/ctl/publish"
	"github.com/switchboard-project/switchboard/internal/ctl/sshexec"
)

const usage = `usage:
  sbctl edge upgrade [-targets FILE] [-target NAME] [-repo DIR] [-ref REF] [-apply]
      Build a committed ref, upload it and run "switchboard upgrade" on the edge.
  sbctl edge config -base FILE [-cloudflare-ranges] [-targets FILE] [-target NAME] [-apply]
      Install the host settings from FILE (listeners, TLS, identity, proxies,
      DNS). Routes and projects stay the edge's: the first install creates its
      runtime file from the routes in effect. -cloudflare-ranges refreshes
      trusted_proxies from Cloudflare's published lists.
  sbctl site publish -site NAME -dir DIST [-host HOST] [-targets FILE] [-target NAME] [-apply]
      Upload a built directory as the site's live build through an SSH tunnel
      with the sites token; with -host, check the public answer and roll back
      on failure. Add the address itself once in the console (Add website).

  sbctl site cutover -host HOST -to IP -cloudflare-token FILE [-dir DIST] [-apply]
  sbctl site cutover -host HOST -back EVIDENCE -cloudflare-token FILE [-apply]
      Move a live hostname (for example an apex served by Cloudflare Pages) to
      the edge: its address records are replaced by a proxied A record in one
      DNS batch, the edge must answer (and serve DIST's index.html), and any
      failure or Ctrl-C puts the previous record back. The replaced records
      are kept under ~/.local/state/sbctl; -back restores them.

Without -apply every command only prints the plan. The targets file lists one
entry per server (see sbctl.example.json).
`

// Cloudflare's published proxy ranges.
var cloudflareLists = []string{"https://www.cloudflare.com/ips-v4", "https://www.cloudflare.com/ips-v6"}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	commands := map[string]func([]string) int{
		"edge upgrade": edgeUpgrade,
		"edge config":  edgeConfig,
		"site publish": sitePublish,
		"site cutover": siteCutover,
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
		fmt.Fprintln(os.Stderr, "sbctl:", err)
		return nil, false
	}
	targets, err := ctl.ParseTargets(file)
	file.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sbctl:", err)
		return nil, false
	}
	targetName, target, err := targets.Pick(*name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sbctl:", err)
		return nil, false
	}
	host, _ := os.Hostname()
	return &common{
		name: targetName, target: target,
		owner:  fmt.Sprintf("sbctl on %s pid %d since %s", host, os.Getpid(), time.Now().UTC().Format(time.RFC3339)),
		remote: sshexec.Remote{Target: target.SSH.Target, Key: target.SSH.Key, Interactive: isTerminal(os.Stdin)},
	}, true
}

func finish(err error) int {
	if err != nil {
		fmt.Fprintln(os.Stderr, "sbctl:", err)
		return 1
	}
	return 0
}

func edgeUpgrade(args []string) int {
	fs := flag.NewFlagSet("edge upgrade", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Switchboard checkout to build from")
	ref := fs.String("ref", "HEAD", "committed ref to build")
	apply := fs.Bool("apply", false, "do it (default: print the plan)")
	c, ok := setup(fs, args)
	if !ok {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	home, _ := os.UserHomeDir()
	builder := sshexec.GitBuilder{Repo: *repo, GOOS: "linux", GOARCH: "amd64", WorkDir: filepath.Join(home, ".cache", "sbctl")}
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
		fmt.Fprintln(os.Stderr, "sbctl: -base is required")
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
		fmt.Fprintln(os.Stderr, "sbctl: -site and -dir are required")
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
		fmt.Fprintln(os.Stderr, "sbctl: -host, -cloudflare-token and exactly one of -to or -back are required")
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
		Evidence: filepath.Join(home, ".local", "state", "sbctl"), Stamp: time.Now().UTC().Format("20060102T150405Z"),
		Public: &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect}}
	return finish(publish.Cutover(ctx, os.Stdout, client, req))
}

func defaultTargets(home string) string {
	if _, err := os.Stat("sbctl.json"); err == nil {
		return "sbctl.json"
	}
	return filepath.Join(home, ".config", "sbctl", "targets.json")
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
