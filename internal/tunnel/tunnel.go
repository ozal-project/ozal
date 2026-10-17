package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/octaviosantacruz/switchboard/internal/config"
	"github.com/octaviosantacruz/switchboard/internal/events"
	"github.com/octaviosantacruz/switchboard/internal/proxy"
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

// Edge accepts one authorized connector. A second connection never evicts an
// active one. Session publication/removal happens under the same lock.
type Edge struct {
	mu      sync.Mutex
	session *yamux.Session
	sink    events.Sink
}

func NewEdge(sink events.Sink) *Edge {
	if sink == nil {
		sink = events.Discard
	}
	return &Edge{sink: sink}
}

func (e *Edge) emit(kind events.Type, code string) {
	e.sink.Emit(events.Event{Time: time.Now(), Type: kind, Code: code})
}

func (e *Edge) Connected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.session != nil && !e.session.IsClosed()
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
			e.emit(TunnelRejected, "handshake_capacity")
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
				e.emit(TunnelRejected, "tls_denied")
				return
			}
			session, err := yamux.Client(conn, multiplexConfig()) // edge opens streams
			if err != nil {
				e.emit(TunnelRejected, "session_failed")
				return
			}
			defer session.Close()
			e.mu.Lock()
			if e.session != nil && !e.session.IsClosed() {
				e.mu.Unlock()
				e.emit(TunnelRejected, "connector_busy")
				return
			}
			e.session = session
			e.mu.Unlock()
			e.emit(TunnelConnected, "connector_authenticated")
			select {
			case <-serveCtx.Done():
			case <-session.CloseChan():
			}
			_ = session.Close()
			e.mu.Lock()
			if e.session == session {
				e.session = nil
			}
			e.mu.Unlock()
			e.emit(TunnelDisconnected, "session_closed")
		}()
	}
}

func (e *Edge) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	s := e.session
	e.mu.Unlock()
	if s == nil {
		return nil, errors.New("connector offline")
	}
	// At most 32 HTTP requests enter this path, less than AcceptBacklog (64).
	// Unacknowledged opens also expire, closing an unresponsive session.
	stream, err := s.OpenStream()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}

// Handler uses the existing trust/routing/forward pipeline. The fixed URL is
// only an HTTP routing label: DialContext never opens an origin TCP socket.
// No idle stream is reused after a connector replacement.
func (e *Edge) Handler(host string) (http.Handler, func(), error) {
	transport := &http.Transport{DialContext: e.dial, DisableKeepAlives: true, ResponseHeaderTimeout: 10 * time.Second}
	handler, err := proxy.New([]config.Route{{Host: host, Target: "http://tunnel.invalid"}}, nil, nil, proxy.WithTransport(transport), proxy.WithEventSink(e.sink))
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
// Reconnection/enrolment/revocation are deliberately left for TUN-002.
func Connect(ctx context.Context, addr string, tlsConfig *tls.Config, handler http.Handler) error {
	if handler == nil || tlsConfig == nil || tlsConfig.InsecureSkipVerify || tlsConfig.ServerName == "" || tlsConfig.RootCAs == nil || len(tlsConfig.Certificates) == 0 {
		return errors.New("verified mutual TLS is required")
	}
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
	err = server.Serve(session)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return errors.New("tunnel connection closed")
	}
	return nil
}
