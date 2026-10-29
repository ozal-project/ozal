package acmedns

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ozal-project/ozal/internal/ratelimit"
)

const (
	defaultRate        = 20 // queries per second per source address
	defaultBurst       = 40
	defaultIdleTimeout = 5 * time.Second
	maxTCPConnections  = 64
	maxQueriesPerConn  = 8
	maxQueryBytes      = 4096
	maxLimiterKeys     = 10000
)

// Config configures the listeners. Addr must name an address explicitly
// (host:port): binding every interface would also try the host's local
// resolver address and fail on most Linux machines.
type Config struct {
	Zone        Zone
	Store       *Store
	Addr        string
	Rate        float64
	Burst       int
	IdleTimeout time.Duration
	Logger      *slog.Logger
}

// Server answers DNS queries for one zone over UDP and TCP on one address.
type Server struct {
	zone    Zone
	store   *Store
	udp     net.PacketConn
	tcp     net.Listener
	limiter *ratelimit.Limiter
	idle    time.Duration
	log     *slog.Logger

	conns   chan struct{}
	dropped atomic.Int64
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	once    sync.Once
}

// Start binds UDP and TCP and serves until ctx ends or Close is called. A
// failure to bind either is returned at once, so a misconfigured edge fails
// at startup instead of silently never answering the CA.
func Start(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("acmedns: a store is required")
	}
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil || host == "" {
		return nil, errors.New("acmedns: the listen address must be host:port with an explicit host")
	}
	if cfg.Rate <= 0 {
		cfg.Rate = defaultRate
	}
	if cfg.Burst <= 0 {
		cfg.Burst = defaultBurst
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaultIdleTimeout
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	var lc net.ListenConfig
	udp, err := lc.ListenPacket(ctx, "udp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("acmedns: listen udp %s: %w", cfg.Addr, err)
	}
	// With port 0 the TCP listener must reuse the port UDP was given.
	if port == "0" {
		_, udpPort, _ := net.SplitHostPort(udp.LocalAddr().String())
		cfg.Addr = net.JoinHostPort(host, udpPort)
	}
	tcp, err := lc.Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		udp.Close()
		return nil, fmt.Errorf("acmedns: listen tcp %s: %w", cfg.Addr, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	s := &Server{
		zone: cfg.Zone, store: cfg.Store, udp: udp, tcp: tcp, idle: cfg.IdleTimeout, log: cfg.Logger,
		limiter: ratelimit.NewLimiter(cfg.Rate, cfg.Burst, time.Minute, ratelimit.WithMaxKeys(maxLimiterKeys)),
		conns:   make(chan struct{}, maxTCPConnections), cancel: cancel,
	}
	s.wg.Add(3)
	go func() { defer s.wg.Done(); s.serveUDP() }()
	go func() { defer s.wg.Done(); s.serveTCP() }()
	go func() { defer s.wg.Done(); <-ctx.Done(); s.shutdown() }()
	s.log.Info("acmedns.listening", "zone", cfg.Zone.Name(), "addr", cfg.Addr)
	return s, nil
}

// Addr is the bound address (the same for UDP and TCP).
func (s *Server) Addr() net.Addr { return s.udp.LocalAddr() }

// Dropped counts queries discarded by the per-source rate limit.
func (s *Server) Dropped() int64 { return s.dropped.Load() }

// Close stops both listeners and waits for in-flight work.
func (s *Server) Close() error {
	s.cancel()
	s.wg.Wait()
	return nil
}

func (s *Server) shutdown() {
	s.once.Do(func() {
		s.udp.Close()
		s.tcp.Close()
	})
}

// allow charges one query to its source. A source over its budget gets no
// reply at all: answering would turn the server into a reflector for a
// spoofed address.
func (s *Server) allow(addr netip.Addr) bool {
	ok, _ := s.limiter.Allow(addr.Unmap().String())
	if !ok {
		s.dropped.Add(1)
	}
	return ok
}

func (s *Server) serveUDP() {
	buf := make([]byte, 1500)
	for {
		n, from, err := s.udp.ReadFrom(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.log.Warn("acmedns.udp_read_failed", "error", err)
			}
			return
		}
		ap, ok := addrPort(from)
		if !ok || !s.allow(ap.Addr()) {
			continue
		}
		if reply := s.zone.Respond(buf[:n], s.store.TXT, true); reply != nil {
			_, _ = s.udp.WriteTo(reply, from)
		}
	}
}

func (s *Server) serveTCP() {
	for {
		conn, err := s.tcp.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.log.Warn("acmedns.tcp_accept_failed", "error", err)
			}
			return
		}
		ap, ok := addrPort(conn.RemoteAddr())
		if !ok || !s.allow(ap.Addr()) {
			conn.Close()
			continue
		}
		select {
		case s.conns <- struct{}{}:
		default:
			conn.Close() // every slot is busy: shed load instead of queueing
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.conns }()
			s.handleTCP(conn)
		}()
	}
}

// handleTCP answers a few length-prefixed queries, then closes. Every read
// has a deadline, so a client that stalls holds a slot only briefly.
func (s *Server) handleTCP(conn net.Conn) {
	defer conn.Close()
	for range maxQueriesPerConn {
		_ = conn.SetDeadline(time.Now().Add(s.idle))
		var prefix [2]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(prefix[:]))
		if n == 0 || n > maxQueryBytes {
			return
		}
		query := make([]byte, n)
		if _, err := io.ReadFull(conn, query); err != nil {
			return
		}
		reply := s.zone.Respond(query, s.store.TXT, false)
		if reply == nil {
			return
		}
		frame := binary.BigEndian.AppendUint16(nil, uint16(len(reply)))
		if _, err := conn.Write(append(frame, reply...)); err != nil {
			return
		}
	}
}

func addrPort(a net.Addr) (netip.AddrPort, bool) {
	switch v := a.(type) {
	case *net.UDPAddr:
		return v.AddrPort(), v.AddrPort().IsValid()
	case *net.TCPAddr:
		return v.AddrPort(), v.AddrPort().IsValid()
	}
	return netip.AddrPort{}, false
}
