package acmedns

import (
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func testZone(t *testing.T) Zone {
	t.Helper()
	z, err := NewZone("acme.example.test", []string{"ns1.example.test", "ns2.example.test"}, "", 7)
	if err != nil {
		t.Fatal(err)
	}
	return z
}

func query(t *testing.T, name string, typ dnsmessage.Type, mutate func(*dnsmessage.Header, *dnsmessage.Question)) []byte {
	t.Helper()
	h := dnsmessage.Header{ID: 0xbeef, RecursionDesired: true}
	q := dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET}
	if mutate != nil {
		mutate(&h, &q)
	}
	b := dnsmessage.NewBuilder(nil, h)
	if err := b.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := b.Question(q); err != nil {
		t.Fatal(err)
	}
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type parsed struct {
	h         dnsmessage.Header
	questions []dnsmessage.Question
	answers   []dnsmessage.Resource
	authority []dnsmessage.Resource
}

func parse(t *testing.T, wire []byte) parsed {
	t.Helper()
	var p dnsmessage.Parser
	h, err := p.Start(wire)
	if err != nil {
		t.Fatalf("unparseable reply: %v", err)
	}
	out := parsed{h: h}
	if out.questions, err = p.AllQuestions(); err != nil {
		t.Fatal(err)
	}
	if out.answers, err = p.AllAnswers(); err != nil {
		t.Fatal(err)
	}
	if out.authority, err = p.AllAuthorities(); err != nil {
		t.Fatal(err)
	}
	return out
}

func lookupOf(values map[string][]string) Lookup {
	return func(name string) []string { return values[name] }
}

func txtValues(rs []dnsmessage.Resource) []string {
	var out []string
	for _, r := range rs {
		if txt, ok := r.Body.(*dnsmessage.TXTResource); ok {
			out = append(out, strings.Join(txt.TXT, ""))
		}
	}
	return out
}

func TestRespondAnswersAChallengeTXT(t *testing.T) {
	z := testZone(t)
	txt := lookupOf(map[string][]string{"x.acme.example.test": {"value-1", "value-2"}})
	got := parse(t, z.Respond(query(t, "x.acme.example.test.", dnsmessage.TypeTXT, nil), txt, true))
	if !got.h.Response || !got.h.Authoritative || got.h.RecursionAvailable || got.h.RCode != dnsmessage.RCodeSuccess {
		t.Fatalf("header = %+v", got.h)
	}
	if got.h.ID != 0xbeef || !got.h.RecursionDesired {
		t.Fatalf("id/rd not echoed: %+v", got.h)
	}
	if v := txtValues(got.answers); len(v) != 2 || v[0] != "value-1" || v[1] != "value-2" {
		t.Fatalf("answers = %v", v)
	}
	for _, a := range got.answers {
		if a.Header.TTL != DefaultTTL {
			t.Fatalf("ttl = %d", a.Header.TTL)
		}
	}
}

func TestRespondPreservesTheCaseOfTheQuestion(t *testing.T) {
	z := testZone(t)
	txt := lookupOf(map[string][]string{"x.acme.example.test": {"v"}})
	got := parse(t, z.Respond(query(t, "X.AcMe.Example.TEST.", dnsmessage.TypeTXT, nil), txt, true))
	if len(got.answers) != 1 || got.answers[0].Header.Name.String() != "X.AcMe.Example.TEST." || got.questions[0].Name.String() != "X.AcMe.Example.TEST." {
		t.Fatalf("0x20 randomization must be echoed: q=%v a=%v", got.questions, got.answers)
	}
}

func TestRespondNegativeAnswers(t *testing.T) {
	z := testZone(t)
	txt := lookupOf(map[string][]string{"x.acme.example.test": {"v"}})
	for name, tc := range map[string]struct {
		qname string
		qtype dnsmessage.Type
		rcode dnsmessage.RCode
	}{
		"unknown name":          {"nothing.acme.example.test.", dnsmessage.TypeTXT, dnsmessage.RCodeNameError},
		"A at an existing name": {"x.acme.example.test.", dnsmessage.TypeA, dnsmessage.RCodeSuccess},
		"AAAA":                  {"x.acme.example.test.", dnsmessage.TypeAAAA, dnsmessage.RCodeSuccess},
		"ANY gives no data":     {"x.acme.example.test.", dnsmessage.TypeALL, dnsmessage.RCodeSuccess},
		"TXT at the apex":       {"acme.example.test.", dnsmessage.TypeTXT, dnsmessage.RCodeSuccess},
		"SOA below the apex":    {"x.acme.example.test.", dnsmessage.TypeSOA, dnsmessage.RCodeSuccess},
		"NS below the apex":     {"x.acme.example.test.", dnsmessage.TypeNS, dnsmessage.RCodeSuccess},
	} {
		got := parse(t, z.Respond(query(t, tc.qname, tc.qtype, nil), txt, true))
		if got.h.RCode != tc.rcode || len(got.answers) != 0 || !got.h.Authoritative {
			t.Errorf("%s: header %+v answers %d", name, got.h, len(got.answers))
		}
		if len(got.authority) != 1 {
			t.Errorf("%s: a negative answer carries the SOA", name)
		}
	}
}

func TestRespondApexSOAAndNS(t *testing.T) {
	z := testZone(t)
	soa := parse(t, z.Respond(query(t, "acme.example.test.", dnsmessage.TypeSOA, nil), lookupOf(nil), true))
	body, ok := soa.answers[0].Body.(*dnsmessage.SOAResource)
	if !ok || body.NS.String() != "ns1.example.test." || body.MBox.String() != "hostmaster.acme.example.test." || body.Serial != 7 {
		t.Fatalf("soa = %+v", soa.answers)
	}
	ns := parse(t, z.Respond(query(t, "acme.example.test.", dnsmessage.TypeNS, nil), lookupOf(nil), true))
	if len(ns.answers) != 2 {
		t.Fatalf("ns answers = %d", len(ns.answers))
	}
}

func TestRespondRefusesEverythingOutsideTheZone(t *testing.T) {
	z := testZone(t)
	txt := lookupOf(map[string][]string{"evilacme.example.test": {"leak"}, "example.test": {"leak"}})
	for _, name := range []string{
		"example.test.", "www.example.test.", "evilacme.example.test.", "xacme.example.test.",
		"acme.example.test.evil.test.", "google.com.", ".",
	} {
		for _, typ := range []dnsmessage.Type{dnsmessage.TypeTXT, dnsmessage.TypeA, dnsmessage.TypeALL} {
			got := parse(t, z.Respond(query(t, name, typ, nil), txt, true))
			if got.h.RCode != dnsmessage.RCodeRefused || len(got.answers) != 0 || got.h.Authoritative || got.h.RecursionAvailable {
				t.Errorf("%s/%v: %+v answers %d", name, typ, got.h, len(got.answers))
			}
		}
	}
}

func TestRespondRefusesOtherClassesAndOpcodes(t *testing.T) {
	z := testZone(t)
	ch := parse(t, z.Respond(query(t, "version.bind.", dnsmessage.TypeTXT, func(_ *dnsmessage.Header, q *dnsmessage.Question) {
		q.Class = dnsmessage.ClassCHAOS
	}), lookupOf(nil), true))
	if ch.h.RCode != dnsmessage.RCodeRefused {
		t.Fatalf("CHAOS: %+v", ch.h)
	}
	update := parse(t, z.Respond(query(t, "x.acme.example.test.", dnsmessage.TypeTXT, func(h *dnsmessage.Header, _ *dnsmessage.Question) {
		h.OpCode = 5 // UPDATE: this server is not a dynamic-update target
	}), lookupOf(map[string][]string{"x.acme.example.test": {"v"}}), true))
	if update.h.RCode != dnsmessage.RCodeNotImplemented || len(update.answers) != 0 {
		t.Fatalf("UPDATE: %+v", update.h)
	}
}

func TestRespondDropsWhatIsNotAQuery(t *testing.T) {
	z := testZone(t)
	if got := z.Respond(nil, lookupOf(nil), true); got != nil {
		t.Error("an empty datagram must get no reply")
	}
	if got := z.Respond([]byte{1, 2, 3}, lookupOf(nil), true); got != nil {
		t.Error("garbage must get no reply")
	}
	// A response must never be answered: that would let two servers loop and
	// would reflect traffic at a spoofed source.
	resp := query(t, "x.acme.example.test.", dnsmessage.TypeTXT, func(h *dnsmessage.Header, _ *dnsmessage.Question) { h.Response = true })
	if got := z.Respond(resp, lookupOf(nil), true); got != nil {
		t.Error("a message with QR set must get no reply")
	}
}

func TestRespondFormErrOnAnythingButOneQuestion(t *testing.T) {
	z := testZone(t)
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 9})
	_ = b.StartQuestions()
	for range 2 {
		_ = b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("x.acme.example.test."), Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET})
	}
	two, _ := b.Finish()
	if got := parse(t, z.Respond(two, lookupOf(nil), true)); got.h.RCode != dnsmessage.RCodeFormatError {
		t.Fatalf("two questions: %+v", got.h)
	}
	b = dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 9})
	none, _ := b.Finish()
	if got := parse(t, z.Respond(none, lookupOf(nil), true)); got.h.RCode != dnsmessage.RCodeFormatError {
		t.Fatalf("no question: %+v", got.h)
	}
}

func TestRespondTruncatesOverUDPButNotOverTCP(t *testing.T) {
	z := testZone(t)
	var many []string
	for i := range 6 {
		many = append(many, strings.Repeat(string(rune('a'+i)), 200))
	}
	txt := lookupOf(map[string][]string{"x.acme.example.test": many})
	q := query(t, "x.acme.example.test.", dnsmessage.TypeTXT, nil)

	udp := z.Respond(q, txt, true)
	if len(udp) > maxUDPAnswer {
		t.Fatalf("udp reply is %d bytes", len(udp))
	}
	if got := parse(t, udp); !got.h.Truncated || len(got.answers) != 0 {
		t.Fatalf("udp: %+v answers %d", got.h, len(got.answers))
	}
	tcp := parse(t, z.Respond(q, txt, false))
	if tcp.h.Truncated || len(tcp.answers) != 6 {
		t.Fatalf("tcp: %+v answers %d", tcp.h, len(tcp.answers))
	}
}

func TestNewZoneValidation(t *testing.T) {
	for name, args := range map[string]struct {
		zone string
		ns   []string
	}{
		"no zone":        {"", []string{"ns1.example.test"}},
		"single label":   {"com", []string{"ns1.example.test"}},
		"no nameserver":  {"acme.example.test", nil},
		"bad nameserver": {"acme.example.test", []string{"bad ns"}},
		"bad zone":       {"acme..example.test", []string{"ns1.example.test"}},
		"wildcard":       {"*.example.test", []string{"ns1.example.test"}},
	} {
		if _, err := NewZone(args.zone, args.ns, "", 1); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	z, err := NewZone("ACME.Example.Test.", []string{"NS1.Example.Test."}, "", 1)
	if err != nil || z.Name() != "acme.example.test" {
		t.Fatalf("normalization: %v %q", err, z.Name())
	}
	if !z.Contains("A.ACME.example.test") || z.Contains("xacme.example.test") || z.Contains("acme.example.test.evil") {
		t.Fatal("Contains must be a label boundary check")
	}
}

func TestStorePublishesAndRetractsExactRecords(t *testing.T) {
	z := testZone(t)
	s := NewStore(z)
	a, err := s.Publish("X.acme.example.test.", "one")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Publish("x.acme.example.test", "two")
	if got := s.TXT("x.acme.example.test"); len(got) != 2 {
		t.Fatalf("txt = %v", got)
	}
	_ = s.Retract(a)
	if got := s.TXT("x.acme.example.test"); len(got) != 1 || got[0] != "two" {
		t.Fatalf("retract removed the wrong record: %v", got)
	}
	if err := s.Retract(a); err != nil {
		t.Fatal("retracting twice must be fine")
	}
	_ = s.Retract(b)
	if len(s.TXT("x.acme.example.test")) != 0 {
		t.Fatal("record left behind")
	}
	for name, tc := range map[string]struct{ name, value string }{
		"outside the zone": {"x.example.test", "v"},
		"the apex":         {"acme.example.test", "v"},
		"lookalike":        {"xacme.example.test", "v"},
		"empty value":      {"x.acme.example.test", ""},
		"long value":       {"x.acme.example.test", strings.Repeat("v", 256)},
	} {
		if _, err := s.Publish(tc.name, tc.value); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for i := range MaxRecords {
		if _, err := s.Publish("x.acme.example.test", strings.Repeat("a", 1)+string(rune('a'+i))); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	if _, err := s.Publish("x.acme.example.test", "overflow"); err == nil {
		t.Fatal("the store must be bounded")
	}
}

func FuzzRespond(f *testing.F) {
	z, _ := NewZone("acme.example.test", []string{"ns1.example.test"}, "", 1)
	txt := func(name string) []string {
		if name == "x.acme.example.test" {
			return []string{"v"}
		}
		return nil
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 1})
	_ = b.StartQuestions()
	_ = b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("x.acme.example.test."), Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET})
	seed, _ := b.Finish()
	f.Add(seed, true)
	f.Add([]byte{}, false)
	f.Add(make([]byte, 12), true)
	f.Fuzz(func(t *testing.T, wire []byte, udp bool) {
		out := z.Respond(wire, txt, udp)
		if udp && len(out) > maxUDPAnswer {
			t.Fatalf("udp reply of %d bytes", len(out))
		}
		var p dnsmessage.Parser
		if out != nil {
			if _, err := p.Start(out); err != nil {
				t.Fatalf("our own reply does not parse: %v", err)
			}
		}
	})
}
