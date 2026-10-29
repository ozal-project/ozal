// Package acmedns is a minimal authoritative DNS server for one zone whose
// only content is DNS-01 challenge TXT records. A domain delegates its
// _acme-challenge name to a name under this zone with one CNAME, and Ozal
// answers for it from memory: no provider account, API or token is involved.
//
// The decision of what to answer (Zone.Respond) is a pure function over
// bytes; the sockets live in server.go.
package acmedns

import (
	"errors"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	// DefaultTTL keeps a stale challenge from outliving its cleanup.
	DefaultTTL = 60
	// maxUDPAnswer is the classic DNS-over-UDP limit. Answers are tiny; a
	// larger one is truncated so the client retries over TCP.
	maxUDPAnswer = 512
)

// Zone is the one zone this server is authoritative for.
type Zone struct {
	name        string // lower-case, no trailing dot
	nameservers []string
	contact     string // SOA RNAME, fully qualified
	serial      uint32
}

// NewZone validates the zone. nameservers are the host names published in
// the zone's NS set (the ones the parent zone delegates to); contact is the
// SOA mailbox as a DNS name and defaults to hostmaster.<zone>.
func NewZone(name string, nameservers []string, contact string, serial uint32) (Zone, error) {
	name = clean(name)
	if !validName(name) || !strings.Contains(name, ".") {
		return Zone{}, errors.New("acmedns: the zone must be a domain name below a public suffix")
	}
	if len(nameservers) == 0 {
		return Zone{}, errors.New("acmedns: at least one nameserver is required")
	}
	ns := make([]string, 0, len(nameservers))
	for _, n := range nameservers {
		n = clean(n)
		if !validName(n) {
			return Zone{}, errors.New("acmedns: nameserver " + n + " is not a valid host name")
		}
		ns = append(ns, n)
	}
	contact = clean(contact)
	if contact == "" {
		contact = "hostmaster." + name
	}
	if !validName(contact) {
		return Zone{}, errors.New("acmedns: contact is not a valid DNS name")
	}
	return Zone{name: name, nameservers: ns, contact: contact, serial: serial}, nil
}

// Name returns the zone name without the trailing dot.
func (z Zone) Name() string { return z.name }

// Contains reports whether name is the zone or below it.
func (z Zone) Contains(name string) bool {
	name = clean(name)
	return name == z.name || strings.HasSuffix(name, "."+z.name)
}

func clean(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

func validName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_'
			if !ok {
				return false
			}
		}
	}
	return true
}

// Lookup returns the TXT values published at a lower-case name.
type Lookup func(name string) []string

// Respond answers one DNS message. It returns nil when no reply must be
// sent (a response, or something that is not a DNS message). It never
// recurses and never answers for a name outside the zone, so it cannot be
// used as an open resolver; every answer is small, and ANY is answered as
// "no data" to give a spoofed source nothing to amplify.
func (z Zone) Respond(query []byte, txt Lookup, udp bool) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil || h.Response {
		return nil
	}
	reply := dnsmessage.Header{ID: h.ID, Response: true, OpCode: h.OpCode, RecursionDesired: h.RecursionDesired}

	questions, err := p.AllQuestions()
	if err != nil || len(questions) != 1 {
		reply.RCode = dnsmessage.RCodeFormatError
		return z.build(reply, nil, nil, nil, udp)
	}
	q := questions[0]
	if h.OpCode != 0 {
		reply.RCode = dnsmessage.RCodeNotImplemented
		return z.build(reply, &q, nil, nil, udp)
	}
	if q.Class != dnsmessage.ClassINET {
		reply.RCode = dnsmessage.RCodeRefused
		return z.build(reply, &q, nil, nil, udp)
	}

	name := clean(q.Name.String())
	if !z.Contains(name) {
		reply.RCode = dnsmessage.RCodeRefused
		return z.build(reply, &q, nil, nil, udp)
	}
	reply.Authoritative = true

	values := txt(name)
	apex := name == z.name
	if !apex && len(values) == 0 {
		reply.RCode = dnsmessage.RCodeNameError
		return z.build(reply, &q, nil, z.soa(), udp)
	}

	var answers []dnsmessage.Resource
	switch q.Type {
	case dnsmessage.TypeTXT:
		for _, v := range values {
			answers = append(answers, dnsmessage.Resource{
				Header: z.header(q.Name),
				Body:   &dnsmessage.TXTResource{TXT: []string{v}},
			})
		}
	case dnsmessage.TypeSOA:
		if apex {
			answers = append(answers, *z.soa())
		}
	case dnsmessage.TypeNS:
		if apex {
			for _, n := range z.nameservers {
				answers = append(answers, dnsmessage.Resource{
					Header: z.header(q.Name),
					Body:   &dnsmessage.NSResource{NS: mustName(n)},
				})
			}
		}
	}
	if len(answers) == 0 {
		return z.build(reply, &q, nil, z.soa(), udp)
	}
	return z.build(reply, &q, answers, nil, udp)
}

func (z Zone) header(name dnsmessage.Name) dnsmessage.ResourceHeader {
	return dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET, TTL: DefaultTTL}
}

func (z Zone) soa() *dnsmessage.Resource {
	return &dnsmessage.Resource{
		Header: z.header(mustName(z.name)),
		Body: &dnsmessage.SOAResource{
			NS: mustName(z.nameservers[0]), MBox: mustName(z.contact),
			Serial: z.serial, Refresh: 3600, Retry: 600, Expire: 86400, MinTTL: DefaultTTL,
		},
	}
}

func mustName(n string) dnsmessage.Name {
	name, err := dnsmessage.NewName(n + ".")
	if err != nil {
		panic("acmedns: validated name rejected: " + err.Error())
	}
	return name
}

// build encodes a reply. Over UDP an answer larger than the classic limit is
// replaced by an empty, truncated one.
func (z Zone) build(h dnsmessage.Header, q *dnsmessage.Question, answers []dnsmessage.Resource, authority *dnsmessage.Resource, udp bool) []byte {
	out, err := encode(h, q, answers, authority)
	if err != nil {
		h.RCode = dnsmessage.RCodeServerFailure
		out, _ = encode(h, q, nil, nil)
		return out
	}
	if udp && len(out) > maxUDPAnswer {
		h.Truncated = true
		out, _ = encode(h, q, nil, nil)
	}
	return out
}

func encode(h dnsmessage.Header, q *dnsmessage.Question, answers []dnsmessage.Resource, authority *dnsmessage.Resource) ([]byte, error) {
	b := dnsmessage.NewBuilder(nil, h)
	if q != nil {
		if err := b.StartQuestions(); err != nil {
			return nil, err
		}
		if err := b.Question(*q); err != nil {
			return nil, err
		}
	}
	if len(answers) > 0 {
		if err := b.StartAnswers(); err != nil {
			return nil, err
		}
		for _, r := range answers {
			if err := addResource(&b, r); err != nil {
				return nil, err
			}
		}
	}
	if authority != nil {
		if err := b.StartAuthorities(); err != nil {
			return nil, err
		}
		if err := addResource(&b, *authority); err != nil {
			return nil, err
		}
	}
	return b.Finish()
}

func addResource(b *dnsmessage.Builder, r dnsmessage.Resource) error {
	switch body := r.Body.(type) {
	case *dnsmessage.TXTResource:
		return b.TXTResource(r.Header, *body)
	case *dnsmessage.SOAResource:
		return b.SOAResource(r.Header, *body)
	case *dnsmessage.NSResource:
		return b.NSResource(r.Header, *body)
	}
	return errors.New("acmedns: unsupported resource")
}
