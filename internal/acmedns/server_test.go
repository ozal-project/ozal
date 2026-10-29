package acmedns

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func startTestServer(t *testing.T, mutate func(*Config)) (*Server, *Store) {
	t.Helper()
	zone := testZone(t)
	store := NewStore(zone)
	cfg := Config{Zone: zone, Store: store, Addr: "127.0.0.1:0", IdleTimeout: 300 * time.Millisecond}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, store
}

func udpQuery(t *testing.T, s *Server, wire []byte) []byte {
	t.Helper()
	conn, err := net.Dial("udp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(wire); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no reply: %v", err)
	}
	return buf[:n]
}

func tcpQueryOn(t *testing.T, conn net.Conn, wire []byte) ([]byte, error) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(wire)))
	if _, err := conn.Write(append(frame, wire...)); err != nil {
		return nil, err
	}
	var prefix [2]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return nil, err
	}
	out := make([]byte, binary.BigEndian.Uint16(prefix[:]))
	_, err := io.ReadFull(conn, out)
	return out, err
}

func TestServerAnswersOverUDPAndTCPOnTheSamePort(t *testing.T) {
	s, store := startTestServer(t, nil)
	if _, err := store.Publish("x.acme.example.test", "challenge-value"); err != nil {
		t.Fatal(err)
	}
	q := query(t, "x.acme.example.test.", dnsmessage.TypeTXT, nil)

	if v := txtValues(parse(t, udpQuery(t, s, q)).answers); len(v) != 1 || v[0] != "challenge-value" {
		t.Fatalf("udp answers = %v", v)
	}
	conn, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reply, err := tcpQueryOn(t, conn, q)
	if err != nil {
		t.Fatal(err)
	}
	if v := txtValues(parse(t, reply).answers); len(v) != 1 || v[0] != "challenge-value" {
		t.Fatalf("tcp answers = %v", v)
	}
}

func TestServerHonoursPublishAndRetractImmediately(t *testing.T) {
	s, store := startTestServer(t, nil)
	q := query(t, "x.acme.example.test.", dnsmessage.TypeTXT, nil)
	if got := parse(t, udpQuery(t, s, q)); got.h.RCode != dnsmessage.RCodeNameError {
		t.Fatalf("before publish: %+v", got.h)
	}
	id, _ := store.Publish("x.acme.example.test", "v")
	if len(txtValues(parse(t, udpQuery(t, s, q)).answers)) != 1 {
		t.Fatal("published record not served")
	}
	_ = store.Retract(id)
	if got := parse(t, udpQuery(t, s, q)); got.h.RCode != dnsmessage.RCodeNameError {
		t.Fatalf("after retract: %+v", got.h)
	}
}

func TestServerIsNotAnOpenResolver(t *testing.T) {
	s, _ := startTestServer(t, nil)
	got := parse(t, udpQuery(t, s, query(t, "www.google.com.", dnsmessage.TypeA, nil)))
	if got.h.RCode != dnsmessage.RCodeRefused || len(got.answers) != 0 || got.h.RecursionAvailable {
		t.Fatalf("%+v", got.h)
	}
}

func TestServerStaysSilentForMalformedAndOverBudgetSources(t *testing.T) {
	s, _ := startTestServer(t, func(c *Config) { c.Rate, c.Burst = 1, 3 })
	conn, err := net.Dial("udp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	q := query(t, "acme.example.test.", dnsmessage.TypeSOA, nil)
	for range 20 {
		_, _ = conn.Write(q)
	}
	replies := 0
	buf := make([]byte, 1500)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if _, err := conn.Read(buf); err != nil {
			break
		}
		replies++
	}
	if replies >= 20 || replies < 1 {
		t.Fatalf("replies = %d of 20: the per-source budget must cap them", replies)
	}
	if s.Dropped() == 0 {
		t.Fatal("no query was counted as dropped")
	}
}

func TestServerTCPLimits(t *testing.T) {
	s, _ := startTestServer(t, nil)
	// A length prefix beyond the bound closes the connection.
	conn, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte{0xff, 0xff})
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("an oversized frame must close the connection")
	}
	conn.Close()

	// A client that connects and says nothing is dropped by the deadline.
	idle, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	_ = idle.SetReadDeadline(time.Now().Add(2 * time.Second))
	start := time.Now()
	if _, err := idle.Read(make([]byte, 1)); err == nil || time.Since(start) > time.Second {
		t.Fatalf("an idle connection must be closed promptly (took %s, err %v)", time.Since(start), err)
	}

	// Only a bounded number of queries are served per connection.
	c2, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	q := query(t, "acme.example.test.", dnsmessage.TypeSOA, nil)
	served := 0
	for range maxQueriesPerConn + 4 {
		if _, err := tcpQueryOn(t, c2, q); err != nil {
			break
		}
		served++
	}
	if served != maxQueriesPerConn {
		t.Fatalf("served %d queries on one connection, want %d", served, maxQueriesPerConn)
	}
}

func TestServerRefusesToBindWithoutAnExplicitHostOrWhenTheAddressIsTaken(t *testing.T) {
	zone := testZone(t)
	store := NewStore(zone)
	if _, err := Start(context.Background(), Config{Zone: zone, Store: store, Addr: ":0"}); err == nil {
		t.Fatal("a wildcard bind must be refused")
	}
	s, _ := startTestServer(t, nil)
	if _, err := Start(context.Background(), Config{Zone: zone, Store: store, Addr: s.Addr().String()}); err == nil {
		t.Fatal("a second server on the same address must fail at startup")
	}
	if _, err := Start(context.Background(), Config{Zone: zone, Addr: "127.0.0.1:0"}); err == nil {
		t.Fatal("a store is required")
	}
}

func TestServerStopsOnContextCancel(t *testing.T) {
	zone := testZone(t)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := Start(ctx, Config{Zone: zone, Store: NewStore(zone), Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	addr := s.Addr().String()
	cancel()
	s.Close()
	if _, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		t.Fatal("the listener is still accepting after shutdown")
	}
}

// TestServerAgreesWithDig checks the wire format against dig, a DNS client
// this repository did not write. It is skipped where dig is not installed.
func TestServerAgreesWithDig(t *testing.T) {
	dig, err := exec.LookPath("dig")
	if err != nil {
		t.Skip("dig is not installed")
	}
	s, store := startTestServer(t, nil)
	if _, err := store.Publish("x.acme.example.test", "challenge-value"); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(s.Addr().String())
	run := func(args ...string) string {
		out, err := exec.Command(dig, append([]string{"@127.0.0.1", "-p", port, "+time=2", "+tries=1", "+norecurse"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("dig %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	for _, proto := range [][]string{{}, {"+tcp"}} {
		out := run(append([]string{"x.acme.example.test", "TXT", "+short"}, proto...)...)
		if strings.TrimSpace(out) != `"challenge-value"` {
			t.Fatalf("dig %v: %q", proto, out)
		}
	}
	full := run("x.acme.example.test", "TXT")
	if !strings.Contains(full, "flags: qr aa") || strings.Contains(full, " ra") {
		t.Fatalf("dig must see an authoritative, non-recursive answer:\n%s", full)
	}
	if out := run("nothing.acme.example.test", "TXT"); !strings.Contains(out, "NXDOMAIN") {
		t.Fatalf("NXDOMAIN expected:\n%s", out)
	}
	if out := run("www.example.org", "A"); !strings.Contains(out, "REFUSED") {
		t.Fatalf("REFUSED expected:\n%s", out)
	}
	if out := run("acme.example.test", "NS", "+short"); !strings.Contains(out, "ns1.example.test.") || !strings.Contains(out, "ns2.example.test.") {
		t.Fatalf("NS set: %q", out)
	}
	if out := run("acme.example.test", "SOA", "+short"); !strings.Contains(out, "ns1.example.test. hostmaster.acme.example.test. 7 ") {
		t.Fatalf("SOA: %q", out)
	}
}
