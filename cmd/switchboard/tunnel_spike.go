package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/proxy"
	"github.com/octaviosantacruz/switchboard/internal/tunnel"
)

// runTunnelSpike intentionally isolates TUN-001 from normal deployments. Its
// loopback-only modes let both roles run without a VPS or production secrets.
func runTunnelSpike(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: tunnel-spike init|edge|connect (local experiment)")
	}
	mode := args[0]
	if mode != "init" && mode != "edge" && mode != "connect" {
		return errors.New("unknown tunnel-spike mode")
	}
	fs := flag.NewFlagSet("tunnel-spike "+mode, flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("pki", "", "directory of disposable lab identities")
	host := fs.String("host", "portfolio.localhost", "one allowed public request host")
	listen := fs.String("listen", "127.0.0.1:0", "edge HTTP address (loopback only, ephemeral by default)")
	tunnelListen := fs.String("tunnel-listen", "127.0.0.1:0", "connector ingress address (loopback only, ephemeral by default)")
	edgeAddr := fs.String("edge", "", "edge connector ingress address (loopback only)")
	root := fs.String("root", "", "absolute static site directory on the connector")
	upstream := fs.String("upstream", "", "fixed HTTP loopback upstream on the connector")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *dir == "" {
		return errors.New("-pki is required")
	}
	if mode == "init" {
		if err := tunnel.InitLab(*dir); err != nil {
			return err
		}
		fmt.Fprintln(out, "Disposable lab identities created (expire in 24 hours); keep directory private.")
		return nil
	}
	normalized, err := config.NormalizeHost(*host)
	if err != nil {
		return errors.New("invalid route host")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	role := mode
	if mode == "connect" {
		role = "connector"
	}
	cert, roots, err := tunnel.LoadIdentity(filepath.Join(*dir, role+".crt"), filepath.Join(*dir, role+".key"), filepath.Join(*dir, "ca.crt"))
	if err != nil {
		return err
	}
	if mode == "connect" {
		if err := validateLabAddress(*edgeAddr, false); err != nil {
			return err
		}
		route, err := labRoute(normalized, *root, *upstream)
		if err != nil {
			return err
		}
		cfg, err := tunnel.ClientTLS(cert, roots, "edge.localhost")
		if err != nil {
			return err
		}
		handler, err := proxy.New([]config.Route{route}, nil, nil, proxy.WithAuthenticatedTunnelTrust())
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "Connecting outbound to the local edge; one fixed route, no inbound connector listener.")
		return tunnel.Connect(ctx, *edgeAddr, cfg, handler)
	}
	if err := validateLabAddress(*listen, true); err != nil {
		return err
	}
	if err := validateLabAddress(*tunnelListen, true); err != nil {
		return err
	}
	cfg, err := tunnel.ServerTLS(cert, roots, "connector.localhost")
	if err != nil {
		return err
	}
	// Event writes happen in multiple goroutines. slog serializes the writer.
	sink := events.SlogSink{Logger: slog.New(slog.NewJSONHandler(out, nil))}
	edge := tunnel.NewEdge(sink)
	handler, closeTransport, err := edge.Handler(normalized)
	if err != nil {
		return err
	}
	defer closeTransport()
	httpListener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer httpListener.Close()
	tunnelListener, err := net.Listen("tcp", *tunnelListen)
	if err != nil {
		return err
	}
	defer tunnelListener.Close()
	fmt.Fprintf(out, "HTTP http://%s (Host: %s)\nTUNNEL %s\n", httpListener.Addr(), normalized, tunnelListener.Addr())
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, MaxHeaderBytes: 64 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	defer server.Close()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 2)
	go func() { done <- edge.Serve(runCtx, tunnelListener, cfg) }()
	go func() {
		err := server.Serve(httpListener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	var result error
	finished := 0
	select {
	case <-ctx.Done():
	case result = <-done:
		finished++
	}
	cancel()
	_ = server.Close()
	// The two workers are joined even if one fails before ctx cancellation.
	for finished < 2 {
		<-done
		finished++
	}
	return result
}

func validateLabAddress(addr string, allowZero bool) error {
	host, port, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	n, portErr := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || n < 0 || n > 65535 || (!allowZero && n == 0) {
		return errors.New("lab address must be a literal loopback IP and valid port")
	}
	return nil
}

func labRoute(host, root, upstream string) (config.Route, error) {
	if (root == "") == (upstream == "") {
		return config.Route{}, errors.New("choose exactly one of -root or -upstream")
	}
	if root != "" {
		if !filepath.IsAbs(root) {
			return config.Route{}, errors.New("-root must be absolute")
		}
		return config.Route{Host: host, Root: root}, nil
	}
	u, err := url.Parse(upstream)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return config.Route{}, errors.New("-upstream must be a fixed HTTP loopback origin")
	}
	if err := validateLabAddress(u.Host, false); err != nil {
		return config.Route{}, err
	}
	return config.Route{Host: host, Target: upstream}, nil
}
