package certs

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

var testTSIGSecret = []byte("0123456789abcdef0123456789abcdef")

type fakeMode int

const (
	modeOK fakeMode = iota
	modeRefuse
	modeServFail
	modeUnsignedOK
	modeBadMAC
	modeWrongKey
	modeSkewed
	modeDrop
)

// fakeUpdateServer is a loopback authoritative server for one zone: it
// verifies the TSIG on every UPDATE, applies add/delete to a TXT set and
// answers signed, or misbehaves in the configured way.
type fakeUpdateServer struct {
	t    *testing.T
	ln   net.Listener
	key  tsigKey
	zone string
	now  time.Time

	mu       sync.Mutex
	mode     fakeMode
	txt      map[string]map[string]bool // name -> value set
	seen     []dnsmessage.Header
	updates  []fakeUpdate
	calls    int
	rejected int
}

type fakeUpdate struct {
	zoneQuestion dnsmessage.Question
	name         string
	class        dnsmessage.Class
	ttl          uint32
	value        string
}

func newFakeUpdateServer(t *testing.T, now time.Time) *fakeUpdateServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := newTSIGKey("ozal-key.", "hmac-sha256", testTSIGSecret)
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeUpdateServer{t: t, ln: ln, key: key, zone: "example.test", now: now, txt: map[string]map[string]bool{}}
	t.Cleanup(func() { ln.Close() })
	go s.serve()
	return s
}

func (s *fakeUpdateServer) setMode(m fakeMode) {
	s.mu.Lock()
	s.mode = m
	s.mu.Unlock()
}

func (s *fakeUpdateServer) has(name, value string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.txt[name][value]
}

func (s *fakeUpdateServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeUpdateServer) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var length [2]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return
	}
	msg := make([]byte, binary.BigEndian.Uint16(length[:]))
	if _, err := io.ReadFull(conn, msg); err != nil {
		return
	}
	s.mu.Lock()
	s.calls++
	mode := s.mode
	s.mu.Unlock()
	if mode == modeDrop {
		return
	}

	reply := s.process(msg, mode)
	if reply == nil {
		return
	}
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(reply)))
	_, _ = conn.Write(append(frame, reply...))
}

// splitSignedRequest returns the request without its trailing TSIG record
// and that record's MAC, after checking the signature with the shared key.
func (s *fakeUpdateServer) splitSignedRequest(msg []byte) (stripped, mac []byte, ok bool) {
	counts := [4]int{}
	for i := range counts {
		counts[i] = int(binary.BigEndian.Uint16(msg[4+2*i:]))
	}
	off := dnsHeaderLen
	var err error
	for range counts[0] {
		if _, off, err = readName(msg, off); err != nil {
			return nil, nil, false
		}
		off += 4
	}
	start := -1
	total := counts[1] + counts[2] + counts[3]
	for i := 0; i < total; i++ {
		recStart := off
		if _, off, err = readName(msg, off); err != nil {
			return nil, nil, false
		}
		rdlen := int(binary.BigEndian.Uint16(msg[off+8:]))
		rtype := binary.BigEndian.Uint16(msg[off:])
		off += 10 + rdlen
		if i == total-1 && rtype == tsigType {
			start = recStart
		}
	}
	if start < 0 {
		return nil, nil, false
	}
	owner, p, _ := readName(msg, start)
	p += 10
	alg, p, _ := readName(msg, p)
	if owner != s.key.name || alg != s.key.algorithm {
		return nil, nil, false
	}
	var signed uint64
	for _, b := range msg[p : p+6] {
		signed = signed<<8 | uint64(b)
	}
	fudge := binary.BigEndian.Uint16(msg[p+6:])
	macLen := int(binary.BigEndian.Uint16(msg[p+8:]))
	mac = msg[p+10 : p+10+macLen]
	stripped = append([]byte(nil), msg[:start]...)
	binary.BigEndian.PutUint16(stripped[10:12], uint16(counts[3]-1))
	vars, err := s.key.variables(time.Unix(int64(signed), 0), fudge, 0, nil)
	if err != nil {
		return nil, nil, false
	}
	if !equalMAC(mac, s.key.mac(nil, stripped, vars)) {
		return nil, nil, false
	}
	return stripped, mac, true
}

func equalMAC(a, b []byte) bool { return string(a) == string(b) }

func (s *fakeUpdateServer) process(msg []byte, mode fakeMode) []byte {
	var p dnsmessage.Parser
	hdr, err := p.Start(msg)
	if err != nil {
		return nil
	}
	stripped, requestMAC, ok := s.splitSignedRequest(msg)
	if !ok {
		s.mu.Lock()
		s.rejected++
		s.mu.Unlock()
		return s.answer(hdr, 9, nil, mode) // NOTAUTH, unsigned
	}
	s.mu.Lock()
	s.seen = append(s.seen, hdr)
	s.mu.Unlock()

	var sp dnsmessage.Parser
	if _, err := sp.Start(stripped); err != nil {
		return nil
	}
	questions, err := sp.AllQuestions()
	if err != nil || len(questions) != 1 {
		return s.answer(hdr, 1, nil, mode)
	}
	if err := sp.SkipAllAnswers(); err != nil {
		return nil
	}
	updates, err := sp.AllAuthorities()
	if err != nil {
		return nil
	}
	if mode == modeRefuse {
		return s.answer(hdr, 5, nil, mode)
	}
	if mode == modeServFail {
		return s.answer(hdr, 2, nil, mode)
	}
	for _, rr := range updates {
		txt, isTXT := rr.Body.(*dnsmessage.TXTResource)
		if !isTXT {
			return s.answer(hdr, 1, nil, mode)
		}
		name := strings.TrimSuffix(strings.ToLower(rr.Header.Name.String()), ".")
		value := strings.Join(txt.TXT, "")
		s.mu.Lock()
		s.updates = append(s.updates, fakeUpdate{zoneQuestion: questions[0], name: name, class: rr.Header.Class, ttl: rr.Header.TTL, value: value})
		switch rr.Header.Class {
		case dnsmessage.ClassINET:
			if s.txt[name] == nil {
				s.txt[name] = map[string]bool{}
			}
			s.txt[name][value] = true
		case classNone:
			delete(s.txt[name], value)
		}
		s.mu.Unlock()
	}
	return s.answer(hdr, 0, requestMAC, mode)
}

// answer builds the reply and, for rcode 0 in the normal modes, signs it.
func (s *fakeUpdateServer) answer(req dnsmessage.Header, rcode dnsmessage.RCode, requestMAC []byte, mode fakeMode) []byte {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: req.ID, Response: true, OpCode: opcodeUpdate, RCode: rcode})
	zoneName := dnsmessage.MustNewName(s.zone + ".")
	_ = b.StartQuestions()
	_ = b.Question(dnsmessage.Question{Name: zoneName, Type: dnsmessage.TypeSOA, Class: dnsmessage.ClassINET})
	msg, err := b.Finish()
	if err != nil {
		s.t.Error(err)
		return nil
	}
	if rcode != 0 || requestMAC == nil || mode == modeUnsignedOK {
		return msg
	}
	key, when := s.key, s.now
	if mode == modeWrongKey {
		key, _ = newTSIGKey("ozal-key.", "hmac-sha256", []byte("fedcba9876543210fedcba9876543210"))
	}
	if mode == modeSkewed {
		when = when.Add(time.Hour)
	}
	vars, _ := key.variables(when, tsigFudge, 0, nil)
	prefix := binary.BigEndian.AppendUint16(nil, uint16(len(requestMAC)))
	prefix = append(prefix, requestMAC...)
	mac := key.mac(prefix, msg, vars)
	if mode == modeBadMAC {
		mac[0] ^= 0xff
	}
	owner, _ := wireName(key.name)
	alg, _ := wireName(key.algorithm)
	rdata := append([]byte(nil), alg...)
	rdata = appendUint48(rdata, uint64(when.Unix()))
	rdata = binary.BigEndian.AppendUint16(rdata, tsigFudge)
	rdata = binary.BigEndian.AppendUint16(rdata, uint16(len(mac)))
	rdata = append(rdata, mac...)
	rdata = append(rdata, msg[0], msg[1])
	rdata = binary.BigEndian.AppendUint16(rdata, 0)
	rdata = binary.BigEndian.AppendUint16(rdata, 0)
	out := append([]byte(nil), msg...)
	out = append(out, owner...)
	out = binary.BigEndian.AppendUint16(out, tsigType)
	out = binary.BigEndian.AppendUint16(out, tsigClassANY)
	out = binary.BigEndian.AppendUint32(out, 0)
	out = binary.BigEndian.AppendUint16(out, uint16(len(rdata)))
	out = append(out, rdata...)
	binary.BigEndian.PutUint16(out[10:12], 1)
	return out
}

func newTestClient(t *testing.T, s *fakeUpdateServer, mutate func(*RFC2136Config)) *RFC2136Client {
	t.Helper()
	cfg := RFC2136Config{
		Server: s.ln.Addr().String(), Zone: "example.test", KeyName: "ozal-key",
		Secret: testTSIGSecret, Timeout: 2 * time.Second, Now: func() time.Time { return s.now },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := NewRFC2136Client(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var fixedNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

const challenge = "gfj9Xq...Rg85nM-Zz5kl2Zfo"

func TestRFC2136CreateAndDeleteExactRecord(t *testing.T) {
	s := newFakeUpdateServer(t, fixedNow)
	c := newTestClient(t, s, nil)
	ctx := context.Background()

	other := "another-challenge-value"
	if _, err := c.CreateTXT(ctx, "_acme-challenge.example.test", other); err != nil {
		t.Fatal(err)
	}
	rec, err := c.CreateTXT(ctx, "_ACME-challenge.Example.test.", challenge)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ZoneID != "example.test" || rec.Name != "_acme-challenge.example.test" || rec.Value != challenge || rec.ID == "" {
		t.Fatalf("record = %+v", rec)
	}
	if !s.has("_acme-challenge.example.test", challenge) || !s.has("_acme-challenge.example.test", other) {
		t.Fatal("create did not reach the server")
	}

	if err := c.DeleteTXT(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if s.has("_acme-challenge.example.test", challenge) {
		t.Fatal("record still present after delete")
	}
	if !s.has("_acme-challenge.example.test", other) {
		t.Fatal("delete removed a record it did not create")
	}
	if err := c.DeleteTXT(ctx, rec); err != nil {
		t.Fatalf("deleting a record already gone must succeed: %v", err)
	}
}

func TestRFC2136MessageShape(t *testing.T) {
	s := newFakeUpdateServer(t, fixedNow)
	c := newTestClient(t, s, nil)
	rec, err := c.CreateTXT(context.Background(), "_acme-challenge.example.test", challenge)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteTXT(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.updates) != 2 {
		t.Fatalf("updates = %d", len(s.updates))
	}
	add, del := s.updates[0], s.updates[1]
	zone := add.zoneQuestion
	if zone.Name.String() != "example.test." || zone.Type != dnsmessage.TypeSOA || zone.Class != dnsmessage.ClassINET {
		t.Fatalf("zone section = %+v", zone)
	}
	if add.class != dnsmessage.ClassINET || add.ttl != 60 {
		t.Fatalf("add = %+v", add)
	}
	if del.class != classNone || del.ttl != 0 || del.value != challenge {
		t.Fatalf("delete must remove the exact RR (class NONE, ttl 0): %+v", del)
	}
	for _, h := range s.seen {
		if h.OpCode != opcodeUpdate {
			t.Fatalf("opcode = %d", h.OpCode)
		}
	}
}

func TestRFC2136RefusesNamesOutsideTheZoneWithoutSending(t *testing.T) {
	s := newFakeUpdateServer(t, fixedNow)
	c := newTestClient(t, s, nil)
	for _, name := range []string{"_acme-challenge.other.test", "example.test.evil.test", "notexample.test"} {
		if _, err := c.CreateTXT(context.Background(), name, challenge); !errors.Is(err, ErrDNS) || errors.Is(err, ErrAmbiguous) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if s.calls != 0 {
		t.Fatalf("server was contacted %d times", s.calls)
	}
}

func TestRFC2136RefusesToDeleteWhatItDidNotCreate(t *testing.T) {
	s := newFakeUpdateServer(t, fixedNow)
	c := newTestClient(t, s, nil)
	good, err := c.CreateTXT(context.Background(), "_acme-challenge.example.test", challenge)
	if err != nil {
		t.Fatal(err)
	}
	calls := s.calls
	for name, rec := range map[string]DNSRecord{
		"no id":         {ZoneID: good.ZoneID, Name: good.Name, Value: good.Value},
		"no value":      {ZoneID: good.ZoneID, ID: good.ID, Name: good.Name},
		"other zone":    {ZoneID: "other.test", ID: good.ID, Name: good.Name, Value: good.Value},
		"forged id":     {ZoneID: good.ZoneID, ID: "rfc2136:0000000000000000", Name: good.Name, Value: good.Value},
		"other value":   {ZoneID: good.ZoneID, ID: good.ID, Name: good.Name, Value: "other"},
		"outside zone":  {ZoneID: good.ZoneID, ID: c.recordID("a.other.test", "x"), Name: "a.other.test", Value: "x"},
		"apex of zone?": {ZoneID: good.ZoneID, ID: good.ID, Name: "example.test", Value: good.Value},
	} {
		if err := c.DeleteTXT(context.Background(), rec); !errors.Is(err, ErrDNS) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if s.calls != calls {
		t.Fatal("a refused delete reached the server")
	}
	if !s.has(good.Name, good.Value) {
		t.Fatal("record was removed")
	}
}

func TestRFC2136ServerOutcomes(t *testing.T) {
	cases := []struct {
		name      string
		mode      fakeMode
		ambiguous bool
	}{
		{"refused is a clean failure", modeRefuse, false},
		{"servfail may have applied", modeServFail, true},
		{"unsigned success proves nothing", modeUnsignedOK, true},
		{"altered signature", modeBadMAC, true},
		{"signed with another key", modeWrongKey, true},
		{"signature outside the time window", modeSkewed, true},
		{"connection closed without an answer", modeDrop, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newFakeUpdateServer(t, fixedNow)
			s.setMode(tc.mode)
			c := newTestClient(t, s, nil)
			_, err := c.CreateTXT(context.Background(), "_acme-challenge.example.test", challenge)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, ErrAmbiguous); got != tc.ambiguous {
				t.Fatalf("ambiguous = %v, want %v (%v)", got, tc.ambiguous, err)
			}
			if !tc.ambiguous && !errors.Is(err, ErrDNS) {
				t.Fatalf("clean failures wrap ErrDNS: %v", err)
			}
		})
	}
}

func TestRFC2136WrongSecretIsRejectedByTheServer(t *testing.T) {
	s := newFakeUpdateServer(t, fixedNow)
	c := newTestClient(t, s, func(cfg *RFC2136Config) { cfg.Secret = []byte("ffffffffffffffffffffffffffffffff") })
	_, err := c.CreateTXT(context.Background(), "_acme-challenge.example.test", challenge)
	if !errors.Is(err, ErrDNS) || errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
	if s.rejected != 1 || s.has("_acme-challenge.example.test", challenge) {
		t.Fatalf("rejected=%d: a badly signed update must not be applied", s.rejected)
	}
}

func TestRFC2136UnreachableServerIsNotAmbiguous(t *testing.T) {
	s := newFakeUpdateServer(t, fixedNow)
	c := newTestClient(t, s, func(cfg *RFC2136Config) {
		cfg.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("connection refused") }
	})
	_, err := c.CreateTXT(context.Background(), "_acme-challenge.example.test", challenge)
	if !errors.Is(err, ErrDNS) || errors.Is(err, ErrAmbiguous) {
		t.Fatalf("nothing was sent, so the outcome is known: %v", err)
	}
}

func TestRFC2136ConfigValidation(t *testing.T) {
	base := RFC2136Config{Server: "127.0.0.1:53", Zone: "example.test", KeyName: "k", Secret: testTSIGSecret}
	bad := map[string]func(*RFC2136Config){
		"no port":        func(c *RFC2136Config) { c.Server = "127.0.0.1" },
		"port zero":      func(c *RFC2136Config) { c.Server = "127.0.0.1:0" },
		"empty server":   func(c *RFC2136Config) { c.Server = ":53" },
		"no zone":        func(c *RFC2136Config) { c.Zone = "" },
		"no key name":    func(c *RFC2136Config) { c.KeyName = "" },
		"short secret":   func(c *RFC2136Config) { c.Secret = []byte("short") },
		"md5":            func(c *RFC2136Config) { c.Algorithm = "hmac-md5.sig-alg.reg.int" },
		"sha1":           func(c *RFC2136Config) { c.Algorithm = "hmac-sha1" },
		"long label":     func(c *RFC2136Config) { c.Zone = strings.Repeat("a", 64) + ".test" },
		"empty zone dot": func(c *RFC2136Config) { c.Zone = "." },
	}
	for name, mutate := range bad {
		cfg := base
		mutate(&cfg)
		if _, err := NewRFC2136Client(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, alg := range []string{"", "hmac-sha256", "HMAC-SHA384.", "hmac-sha512"} {
		cfg := base
		cfg.Algorithm = alg
		if _, err := NewRFC2136Client(cfg); err != nil {
			t.Errorf("%q: %v", alg, err)
		}
	}
}

func TestRFC2136RejectsOversizedValue(t *testing.T) {
	s := newFakeUpdateServer(t, fixedNow)
	c := newTestClient(t, s, nil)
	for _, value := range []string{"", strings.Repeat("x", 256)} {
		if _, err := c.CreateTXT(context.Background(), "_acme-challenge.example.test", value); !errors.Is(err, ErrDNS) {
			t.Fatalf("%d bytes: %v", len(value), err)
		}
	}
	if s.calls != 0 {
		t.Fatal("an invalid value reached the server")
	}
}

func FuzzVerifyTSIGResponse(f *testing.F) {
	key, _ := newTSIGKey("k.", "hmac-sha256", testTSIGSecret)
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 7, Response: true, OpCode: opcodeUpdate})
	_ = b.StartQuestions()
	_ = b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("example.test."), Type: dnsmessage.TypeSOA, Class: dnsmessage.ClassINET})
	msg, _ := b.Finish()
	signed, mac, _ := key.sign(msg, fixedNow)
	f.Add(signed, mac)
	f.Add(msg, mac)
	f.Add([]byte{}, []byte{})
	f.Add(make([]byte, 40), mac)
	f.Fuzz(func(t *testing.T, resp, requestMAC []byte) {
		// Untrusted network input: it may fail, it must not panic.
		_ = key.verifyResponse(requestMAC, resp, fixedNow)
	})
}

// TestRFC2136InteroperatesWithNsupdate checks the TSIG implementation
// against BIND's own client, an implementation this repository did not
// write: nsupdate signs its request (the fake server verifies it) and
// verifies the fake server's signed answer. It is skipped where nsupdate is
// not installed.
func TestRFC2136InteroperatesWithNsupdate(t *testing.T) {
	bin, err := exec.LookPath("nsupdate")
	if err != nil {
		t.Skip("nsupdate is not installed")
	}
	// nsupdate signs with the wall clock, so the server uses it too.
	s := newFakeUpdateServer(t, time.Now())
	_, port, _ := net.SplitHostPort(s.ln.Addr().String())
	secret := base64.StdEncoding.EncodeToString(testTSIGSecret)

	run := func(commands string) (string, error) {
		cmd := exec.Command(bin, "-v", "-y", "hmac-sha256:ozal-key:"+secret)
		cmd.Stdin = strings.NewReader(commands)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("server 127.0.0.1 " + port + "\nzone example.test\nupdate add _acme-challenge.example.test 60 TXT \"" + challenge + "\"\nsend\n")
	if err != nil {
		t.Fatalf("nsupdate add: %v\n%s", err, out)
	}
	if !s.has("_acme-challenge.example.test", challenge) {
		t.Fatalf("nsupdate's signed update was not accepted\n%s", out)
	}
	out, err = run("server 127.0.0.1 " + port + "\nzone example.test\nupdate delete _acme-challenge.example.test 0 TXT \"" + challenge + "\"\nsend\n")
	if err != nil {
		t.Fatalf("nsupdate delete: %v\n%s", err, out)
	}
	if s.has("_acme-challenge.example.test", challenge) {
		t.Fatal("nsupdate's delete was not applied")
	}
	if s.rejected != 0 {
		t.Fatalf("rejected = %d", s.rejected)
	}

	// A server that signs with another key must make nsupdate fail: this
	// proves the run above really authenticated the answer.
	s.setMode(modeWrongKey)
	if out, err := run("server 127.0.0.1 " + port + "\nzone example.test\nupdate add _acme-challenge.example.test 60 TXT \"x\"\nsend\n"); err == nil {
		t.Fatalf("nsupdate accepted an answer signed with the wrong key\n%s", out)
	}
}
