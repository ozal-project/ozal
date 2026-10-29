package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/ozal-project/ozal/internal/config"
	"github.com/ozal-project/ozal/internal/events"
	"github.com/ozal-project/ozal/internal/proxy"
)

const (
	TunnelConnected    events.Type = "tunnel.connected"
	TunnelDisconnected events.Type = "tunnel.disconnected"
	TunnelRejected     events.Type = "tunnel.rejected"
)

func multiplexConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.LogOutput = io.Discard // never log raw peer frames
	c.AcceptBacklog = 64
	c.ConnectionWriteTimeout = 5 * time.Second
	c.StreamOpenTimeout = 5 * time.Second
	c.KeepAliveInterval = 5 * time.Second
	return c
}

// defaultConnector names the single connector of the registry-less lab mode.
const defaultConnector = "default"

type liveSession struct {
	session *yamux.Session
	fp      Fingerprint
	since   time.Time
}

// Edge accepts authorized connectors, at most one live session per connector
// name. A second connection with the same name never evicts the active one.
// Session publication/removal happens under the same lock.
//
// Without an Authorizer (the TUN-001 lab) any certificate that passes the TLS
// checks attaches as "default". With one, the leaf certificate must be an
// enrolled, non-revoked connector, and revocation closes a live session.
type Edge struct {
	mu           sync.Mutex
	sessions     map[string]*liveSession
	sink         events.Sink
	auth         Authorizer
	revalidate   time.Duration
	requestSlots chan struct{}
}

func NewEdge(sink events.Sink) *Edge {
	if sink == nil {
		sink = events.Discard
	}
	return &Edge{sink: sink, sessions: map[string]*liveSession{}, revalidate: 5 * time.Second, requestSlots: make(chan struct{}, 32)}
}

// UseAuthorizer turns on registry-based authorization. Call before Serve.
func (e *Edge) UseAuthorizer(a Authorizer) { e.auth = a }

func (e *Edge) emit(kind events.Type, code, connector string) {
	e.sink.Emit(events.Event{Time: time.Now(), Type: kind, Code: code, Actor: connector})
}

// Connected reports whether any connector is attached.
func (e *Edge) Connected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, l := range e.sessions {
		if !l.session.IsClosed() {
			return true
		}
	}
	return false
}

// ConnectorStatus is one attached connector.
type ConnectorStatus struct {
	Name  string
	Since time.Time
}

// Status lists the attached connectors, sorted by name.
func (e *Edge) Status() []ConnectorStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]ConnectorStatus, 0, len(e.sessions))
	for name, l := range e.sessions {
		if !l.session.IsClosed() {
			out = append(out, ConnectorStatus{Name: name, Since: l.since})
		}
	}
	slices.SortFunc(out, func(a, b ConnectorStatus) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Serve owns listener and all accepted sessions until ctx ends. Handshakes
// are bounded and cancelled on shutdown; no per-peer goroutine lives forever.
func (e *Edge) Serve(ctx context.Context, listener net.Listener, tlsConfig *tls.Config) error {
	if tlsConfig == nil || tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert || tlsConfig.VerifyConnection == nil || tlsConfig.MinVersion < tls.VersionTLS13 {
		return errors.New("mutually authenticated TLS is required")
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 16)
	serveCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(serveCtx, func() { _ = listener.Close() })
	defer func() { cancel(); stop(); _ = listener.Close(); wg.Wait() }()
	if e.auth != nil {
		wg.Add(1)
		go func() { defer wg.Done(); e.revalidateLoop(serveCtx) }()
	}
	for {
		raw, err := listener.Accept()
		if err != nil {
			if serveCtx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = raw.Close()
			e.emit(TunnelRejected, "handshake_capacity", "")
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			defer raw.Close()
			conn := tls.Server(raw, tlsConfig)
			handshakeCtx, handshakeCancel := context.WithTimeout(serveCtx, 5*time.Second)
			err := conn.HandshakeContext(handshakeCtx)
			handshakeCancel()
			if err != nil {
				e.emit(TunnelRejected, "tls_denied", "")
				return
			}
			name, fp, ok := e.authorize(serveCtx, conn)
			if !ok {
				return
			}
			session, err := yamux.Client(conn, multiplexConfig()) // edge opens streams
			if err != nil {
				e.emit(TunnelRejected, "session_failed", name)
				return
			}
			defer session.Close()
			e.mu.Lock()
			if cur := e.sessions[name]; cur != nil && !cur.session.IsClosed() {
				e.mu.Unlock()
				e.emit(TunnelRejected, "connector_busy", name)
				return
			}
			e.sessions[name] = &liveSession{session: session, fp: fp, since: time.Now()}
			e.mu.Unlock()
			e.emit(TunnelConnected, "connector_authenticated", name)
			select {
			case <-serveCtx.Done():
			case <-session.CloseChan():
			}
			_ = session.Close()
			e.mu.Lock()
			if cur := e.sessions[name]; cur != nil && cur.session == session {
				delete(e.sessions, name)
			}
			e.mu.Unlock()
			e.emit(TunnelDisconnected, "session_closed", name)
		}()
	}
}

// authorize names the connector behind a completed handshake. The TLS layer
// already proved the certificate chains to our CA; this decides whether that
// certificate is an enrolled connector.
func (e *Edge) authorize(ctx context.Context, conn *tls.Conn) (string, Fingerprint, bool) {
	var fp Fingerprint
	peers := conn.ConnectionState().PeerCertificates
	if len(peers) > 0 {
		fp = FingerprintOf(peers[0].Raw)
	}
	if e.auth == nil {
		return defaultConnector, fp, true
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	name, ok, err := e.auth.Lookup(lookupCtx, fp)
	switch {
	case err != nil:
		e.emit(TunnelRejected, "registry_unavailable", "")
	case !ok:
		e.emit(TunnelRejected, "connector_unauthorized", "")
	default:
		return name, fp, true
	}
	return "", fp, false
}

// revalidateLoop closes the session of any connector that was revoked after it
// attached, so revocation takes effect without waiting for a reconnect. A
// registry error leaves sessions alone: an unreachable database must not drop
// healthy tunnels.
func (e *Edge) revalidateLoop(ctx context.Context) {
	tick := time.NewTicker(e.revalidate)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		e.mu.Lock()
		snapshot := make(map[string]*liveSession, len(e.sessions))
		for name, l := range e.sessions {
			snapshot[name] = l
		}
		e.mu.Unlock()
		for name, l := range snapshot {
			lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			got, ok, err := e.auth.Lookup(lookupCtx, l.fp)
			cancel()
			if err == nil && (!ok || got != name) {
				e.emit(TunnelRejected, "connector_revoked", name)
				_ = l.session.Close()
			}
		}
	}
}

func (e *Edge) dialer(name string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e.mu.Lock()
		l := e.sessions[name]
		e.mu.Unlock()
		if l == nil {
			return nil, errors.New("connector offline")
		}
		// At most 32 HTTP requests enter this path, less than AcceptBacklog
		// (64). Unacknowledged opens also expire, closing an unresponsive
		// session.
		stream, err := l.session.OpenStream()
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			_ = stream.Close()
			return nil, err
		}
		return stream, nil
	}
}

// Handler uses the existing trust/routing/forward pipeline. The fixed URL is
// only an HTTP routing label: DialContext never opens an origin TCP socket.
// No idle stream is reused after a connector replacement.
func (e *Edge) Handler(host string) (http.Handler, func(), error) {
	return e.HandlerFor(host, defaultConnector)
}

// HandlerFor routes host to the connector enrolled under the given name.
func (e *Edge) HandlerFor(host, connector string) (http.Handler, func(), error) {
	transport := &http.Transport{DialContext: e.dialer(connector), DisableKeepAlives: true, ResponseHeaderTimeout: 10 * time.Second}
	handler, err := proxy.New([]config.Route{{Host: host, Target: "http://tunnel.invalid"}}, nil, nil, proxy.WithTransport(transport), proxy.WithEventSink(e.sink), proxy.WithVisitorAssertion())
	if err != nil {
		return nil, nil, err
	}
	slots := make(chan struct{}, 32)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "tunnel capacity reached", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}), transport.CloseIdleConnections, nil
}

// Connect dials out and serves HTTP over the edge-opened streams. There is no
// inbound homelab listener and the edge cannot choose another local target.
// It makes a single attempt; use ConnectLoop to stay attached across failures.
func Connect(ctx context.Context, addr string, tlsConfig *tls.Config, handler http.Handler) error {
	if err := validateClient(tlsConfig, handler); err != nil {
		return err
	}
	return connect(ctx, addr, tlsConfig, handler, nil)
}

// connect runs one attempt. onUp, if set, is called once the multiplexed
// session is established (the link is usable), on the calling goroutine.
func connect(ctx context.Context, addr string, tlsConfig *tls.Config, handler http.Handler, onUp func()) error {
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: tlsConfig}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return errors.New("tunnel TLS connection failed")
	}
	defer conn.Close()
	session, err := yamux.Server(conn, multiplexConfig())
	if err != nil {
		return errors.New("tunnel session failed")
	}
	defer session.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, MaxHeaderBytes: 64 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	stop := context.AfterFunc(ctx, func() { _ = server.Close(); _ = session.Close() })
	defer stop()
	defer server.Close()
	if onUp != nil {
		onUp()
	}
	err = server.Serve(session)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return errors.New("tunnel connection closed")
	}
	return nil
}
