package certs

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	opcodeUpdate   = 5
	classNone      = dnsmessage.Class(254)
	rfc2136TTL     = 60
	maxTXTString   = 255
	maxDNSTCPFrame = 65535
)

// RFC2136Config points the adapter at an authoritative server that accepts
// signed dynamic updates (BIND, PowerDNS, Knot). Zone is explicit: the
// adapter never guesses where a name lives and refuses names outside it.
// Dial and Now exist so tests use a loopback server and a fixed clock.
type RFC2136Config struct {
	Server    string // host:port, TCP
	Zone      string
	KeyName   string
	Algorithm string // hmac-sha256 (default), hmac-sha384, hmac-sha512
	Secret    []byte // decoded TSIG secret
	Timeout   time.Duration
	Dial      func(ctx context.Context, network, address string) (net.Conn, error)
	Now       func() time.Time
}

// RFC2136Client creates and deletes DNS-01 TXT records with signed DNS
// UPDATE messages (RFC 2136, authenticated by TSIG, RFC 8945) over TCP. It
// implements DNSProvider. The protocol has no record IDs, so the exact
// record is identified by its name and value: a delete removes that one
// record from the set and never the whole set.
type RFC2136Client struct {
	server  string
	zone    string // lower-case, no trailing dot
	key     tsigKey
	timeout time.Duration
	dial    func(ctx context.Context, network, address string) (net.Conn, error)
	now     func() time.Time
	id      uint16
}

// NewRFC2136Client validates the configuration at composition time.
func NewRFC2136Client(cfg RFC2136Config) (*RFC2136Client, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(cfg.Server))
	if err != nil || host == "" {
		return nil, errors.New("certs: rfc2136 server must be host:port")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, errors.New("certs: rfc2136 server port is not valid")
	}
	zone := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.Zone)), ".")
	if zone == "" || strings.Contains(zone, "..") {
		return nil, errors.New("certs: rfc2136 requires the zone that accepts updates")
	}
	if _, err := wireName(zone); err != nil {
		return nil, errors.New("certs: rfc2136 zone is not a valid domain name")
	}
	algorithm := cfg.Algorithm
	if algorithm == "" {
		algorithm = "hmac-sha256"
	}
	key, err := newTSIGKey(cfg.KeyName, algorithm, cfg.Secret)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Dial == nil {
		dialer := &net.Dialer{}
		cfg.Dial = dialer.DialContext
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &RFC2136Client{
		server: net.JoinHostPort(host, port), zone: zone, key: key,
		timeout: cfg.Timeout, dial: cfg.Dial, now: cfg.Now,
		id: uint16(cfg.Now().UnixNano()),
	}, nil
}

func (c *RFC2136Client) recordID(name, value string) string {
	sum := sha256.Sum256([]byte(name + "\x00" + value))
	return "rfc2136:" + hex.EncodeToString(sum[:8])
}

func (c *RFC2136Client) inZone(name string) bool {
	return name == c.zone || strings.HasSuffix(name, "."+c.zone)
}

// CreateTXT adds the challenge record. A failure after the update was sent
// without an authenticated answer is ErrAmbiguous: the server may have
// applied it.
func (c *RFC2136Client) CreateTXT(ctx context.Context, name, value string) (DNSRecord, error) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if !c.inZone(name) {
		return DNSRecord{}, fmt.Errorf("%w: %s is outside the configured zone %s", ErrDNS, name, c.zone)
	}
	if err := c.update(ctx, name, value, false); err != nil {
		return DNSRecord{}, err
	}
	return DNSRecord{ZoneID: c.zone, ID: c.recordID(name, value), Name: name, Value: value}, nil
}

// DeleteTXT removes exactly the record CreateTXT returned. Removing a record
// that is already gone succeeds, as RFC 2136 specifies.
func (c *RFC2136Client) DeleteTXT(ctx context.Context, record DNSRecord) error {
	name := strings.TrimSuffix(strings.ToLower(record.Name), ".")
	if record.ZoneID != c.zone || record.ID == "" || record.Value == "" || !c.inZone(name) || record.ID != c.recordID(name, record.Value) {
		return fmt.Errorf("%w: refusing to delete an unidentified record", ErrDNS)
	}
	return c.update(ctx, name, record.Value, true)
}

func (c *RFC2136Client) update(ctx context.Context, name, value string, remove bool) error {
	if len(value) == 0 || len(value) > maxTXTString {
		return fmt.Errorf("%w: txt value must be 1 to %d bytes", ErrDNS, maxTXTString)
	}
	zoneName, err := dnsmessage.NewName(c.zone + ".")
	if err != nil {
		return fmt.Errorf("%w: zone name: %v", ErrDNS, err)
	}
	owner, err := dnsmessage.NewName(name + ".")
	if err != nil {
		return fmt.Errorf("%w: record name: %v", ErrDNS, err)
	}
	c.id++
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: c.id, OpCode: opcodeUpdate})
	if err := b.StartQuestions(); err != nil {
		return err
	}
	if err := b.Question(dnsmessage.Question{Name: zoneName, Type: dnsmessage.TypeSOA, Class: dnsmessage.ClassINET}); err != nil {
		return err
	}
	if err := b.StartAnswers(); err != nil { // prerequisites: none
		return err
	}
	if err := b.StartAuthorities(); err != nil {
		return err
	}
	header := dnsmessage.ResourceHeader{Name: owner, Class: dnsmessage.ClassINET, TTL: rfc2136TTL}
	if remove {
		header.Class, header.TTL = classNone, 0 // delete this exact RR from the set
	}
	if err := b.TXTResource(header, dnsmessage.TXTResource{TXT: []string{value}}); err != nil {
		return err
	}
	msg, err := b.Finish()
	if err != nil {
		return err
	}
	signed, requestMAC, err := c.key.sign(msg, c.now())
	if err != nil {
		return err
	}

	resp, err := c.exchange(ctx, signed)
	if errors.Is(err, errNotSent) {
		return fmt.Errorf("%w: rfc2136 update of %s: %v", ErrDNS, name, err)
	}
	if err != nil {
		return fmt.Errorf("%w: rfc2136 update of %s: %v", ErrAmbiguous, name, err)
	}
	return c.interpret(c.id, name, requestMAC, resp)
}

func (c *RFC2136Client) interpret(id uint16, name string, requestMAC, resp []byte) error {
	var p dnsmessage.Parser
	hdr, err := p.Start(resp)
	if err != nil || !hdr.Response || hdr.ID != id || hdr.OpCode != opcodeUpdate {
		return fmt.Errorf("%w: rfc2136 update of %s: unexpected response", ErrAmbiguous, name)
	}
	switch hdr.RCode {
	case dnsmessage.RCodeSuccess:
		if err := c.key.verifyResponse(requestMAC, resp, c.now()); err != nil {
			// Success that cannot be authenticated proves nothing.
			return fmt.Errorf("%w: rfc2136 update of %s: %v", ErrAmbiguous, name, err)
		}
		return nil
	case dnsmessage.RCodeServerFailure:
		return fmt.Errorf("%w: rfc2136 update of %s: server failure", ErrAmbiguous, name)
	default:
		return fmt.Errorf("%w: rfc2136 update of %s refused by the server (rcode %d)", ErrDNS, name, hdr.RCode)
	}
}

// errNotSent marks a failure before any byte reached the server, so the
// update cannot have been applied.
var errNotSent = errors.New("could not reach the server")

// exchange sends one length-prefixed message over TCP and reads one reply.
func (c *RFC2136Client) exchange(ctx context.Context, msg []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	conn, err := c.dial(ctx, "tcp", c.server)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNotSent, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(msg)))
	if _, err := conn.Write(append(frame, msg...)); err != nil {
		return nil, err
	}
	var length [2]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(length[:]))
	if n < dnsHeaderLen || n > maxDNSTCPFrame {
		return nil, errors.New("invalid response length")
	}
	resp := make([]byte, n)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

var _ DNSProvider = (*RFC2136Client)(nil)
