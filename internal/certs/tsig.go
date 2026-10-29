package certs

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"
)

// tsigFudge is the clock skew, in seconds, a signature tolerates (RFC 8945
// recommends 300).
const tsigFudge = 300

const (
	tsigType     = 250
	tsigClassANY = 255
	dnsHeaderLen = 12
)

// tsigKey signs DNS messages with a shared secret (RFC 8945). Only the HMAC
// algorithms with a SHA-2 digest are accepted: HMAC-MD5 and HMAC-SHA1 are
// not offered, so a configuration cannot select them by accident.
type tsigKey struct {
	name      string // lower-case fully qualified name, trailing dot
	algorithm string // lower-case fully qualified algorithm name
	secret    []byte
	digest    func() hash.Hash
}

func newTSIGKey(name, algorithm string, secret []byte) (tsigKey, error) {
	name = fqdn(name)
	if _, err := wireName(name); err != nil || name == "." {
		return tsigKey{}, errors.New("certs: tsig key name is not a valid domain name")
	}
	var digest func() hash.Hash
	algorithm = fqdn(algorithm)
	switch algorithm {
	case "hmac-sha256.":
		digest = sha256.New
	case "hmac-sha384.":
		digest = sha512.New384
	case "hmac-sha512.":
		digest = sha512.New
	default:
		return tsigKey{}, fmt.Errorf("certs: tsig algorithm %q is not supported (use hmac-sha256, hmac-sha384 or hmac-sha512)", strings.TrimSuffix(algorithm, "."))
	}
	if len(secret) < 16 {
		return tsigKey{}, errors.New("certs: tsig secret is shorter than 16 bytes")
	}
	return tsigKey{name: name, algorithm: algorithm, secret: secret, digest: digest}, nil
}

func fqdn(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	return name
}

// wireName encodes a name in uncompressed, lower-case wire form.
func wireName(name string) ([]byte, error) {
	name = fqdn(name)
	if name == "." {
		return []byte{0}, nil
	}
	var out []byte
	for label := range strings.SplitSeq(strings.TrimSuffix(name, "."), ".") {
		if label == "" || len(label) > 63 {
			return nil, errors.New("invalid label")
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	if len(out)+1 > 255 {
		return nil, errors.New("name too long")
	}
	return append(out, 0), nil
}

// tsigVariables is the part of the digest that describes the signature
// itself: owner, class, TTL, algorithm, time, fudge, error and other data.
func (k tsigKey) variables(signed time.Time, fudge, tsigErr uint16, other []byte) ([]byte, error) {
	owner, err := wireName(k.name)
	if err != nil {
		return nil, err
	}
	alg, err := wireName(k.algorithm)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), owner...)
	out = binary.BigEndian.AppendUint16(out, tsigClassANY)
	out = binary.BigEndian.AppendUint32(out, 0)
	out = append(out, alg...)
	out = appendUint48(out, uint64(signed.Unix()))
	out = binary.BigEndian.AppendUint16(out, fudge)
	out = binary.BigEndian.AppendUint16(out, tsigErr)
	out = binary.BigEndian.AppendUint16(out, uint16(len(other)))
	return append(out, other...), nil
}

func appendUint48(b []byte, v uint64) []byte {
	return append(b, byte(v>>40), byte(v>>32), byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (k tsigKey) mac(prefix, msg, variables []byte) []byte {
	h := hmac.New(k.digest, k.secret)
	h.Write(prefix)
	h.Write(msg)
	h.Write(variables)
	return h.Sum(nil)
}

// sign appends the TSIG record to a message that has none and returns the
// signed message with the MAC the server must echo back.
func (k tsigKey) sign(msg []byte, now time.Time) (signed, mac []byte, err error) {
	if len(msg) < dnsHeaderLen {
		return nil, nil, errors.New("certs: short dns message")
	}
	vars, err := k.variables(now, tsigFudge, 0, nil)
	if err != nil {
		return nil, nil, err
	}
	mac = k.mac(nil, msg, vars)
	owner, err := wireName(k.name)
	if err != nil {
		return nil, nil, err
	}
	alg, err := wireName(k.algorithm)
	if err != nil {
		return nil, nil, err
	}
	rdata := append([]byte(nil), alg...)
	rdata = appendUint48(rdata, uint64(now.Unix()))
	rdata = binary.BigEndian.AppendUint16(rdata, tsigFudge)
	rdata = binary.BigEndian.AppendUint16(rdata, uint16(len(mac)))
	rdata = append(rdata, mac...)
	rdata = append(rdata, msg[0], msg[1]) // original ID
	rdata = binary.BigEndian.AppendUint16(rdata, 0)
	rdata = binary.BigEndian.AppendUint16(rdata, 0)

	signed = append([]byte(nil), msg...)
	signed = append(signed, owner...)
	signed = binary.BigEndian.AppendUint16(signed, tsigType)
	signed = binary.BigEndian.AppendUint16(signed, tsigClassANY)
	signed = binary.BigEndian.AppendUint32(signed, 0)
	signed = binary.BigEndian.AppendUint16(signed, uint16(len(rdata)))
	signed = append(signed, rdata...)
	binary.BigEndian.PutUint16(signed[10:12], binary.BigEndian.Uint16(signed[10:12])+1)
	return signed, mac, nil
}

// readName decodes a possibly compressed name starting at off and returns
// it with the offset of the first byte after it in the original position.
func readName(msg []byte, off int) (name string, next int, err error) {
	var labels []string
	next = -1
	for hops := 0; ; hops++ {
		if hops > 64 || off >= len(msg) {
			return "", 0, errors.New("bad name")
		}
		c := int(msg[off])
		switch {
		case c == 0:
			if next < 0 {
				next = off + 1
			}
			return strings.ToLower(strings.Join(labels, ".") + "."), next, nil
		case c&0xC0 == 0xC0:
			if off+1 >= len(msg) {
				return "", 0, errors.New("bad name pointer")
			}
			if next < 0 {
				next = off + 2
			}
			off = (c&0x3F)<<8 | int(msg[off+1])
		case c&0xC0 != 0:
			return "", 0, errors.New("bad label type")
		default:
			if off+1+c > len(msg) {
				return "", 0, errors.New("truncated label")
			}
			labels = append(labels, string(msg[off+1:off+1+c]))
			off += 1 + c
		}
	}
}

// verifyResponse authenticates a response to a request signed with
// requestMAC. It requires the last additional record to be a TSIG made with
// this key, over this request, inside the fudge window.
func (k tsigKey) verifyResponse(requestMAC, resp []byte, now time.Time) error {
	if len(resp) < dnsHeaderLen {
		return errors.New("short response")
	}
	counts := [4]int{}
	for i := range counts {
		counts[i] = int(binary.BigEndian.Uint16(resp[4+2*i:]))
	}
	off := dnsHeaderLen
	var err error
	for i := 0; i < counts[0]; i++ { // questions
		if _, off, err = readName(resp, off); err != nil {
			return err
		}
		off += 4
	}
	tsigStart := -1
	for i := 0; i < counts[1]+counts[2]+counts[3]; i++ {
		start := off
		if _, off, err = readName(resp, off); err != nil {
			return err
		}
		if off+10 > len(resp) {
			return errors.New("truncated record")
		}
		rtype := binary.BigEndian.Uint16(resp[off:])
		rdlen := int(binary.BigEndian.Uint16(resp[off+8:]))
		off += 10
		if off+rdlen > len(resp) {
			return errors.New("truncated record data")
		}
		if i == counts[1]+counts[2]+counts[3]-1 && rtype == tsigType {
			tsigStart = start
		}
		off += rdlen
	}
	if tsigStart < 0 || counts[3] == 0 {
		return errors.New("response is not signed")
	}

	owner, p, err := readName(resp, tsigStart)
	if err != nil {
		return err
	}
	if owner != k.name {
		return errors.New("response signed with another key")
	}
	if binary.BigEndian.Uint16(resp[p+2:]) != tsigClassANY {
		return errors.New("tsig class is not ANY")
	}
	p += 10 // type, class, ttl, rdlength
	alg, p, err := readName(resp, p)
	if err != nil {
		return err
	}
	if alg != k.algorithm {
		return errors.New("response signed with another algorithm")
	}
	if p+10 > len(resp) {
		return errors.New("truncated tsig")
	}
	var signed uint64
	for _, b := range resp[p : p+6] {
		signed = signed<<8 | uint64(b)
	}
	fudge := binary.BigEndian.Uint16(resp[p+6:])
	macLen := int(binary.BigEndian.Uint16(resp[p+8:]))
	p += 10
	if p+macLen+6 > len(resp) {
		return errors.New("truncated tsig mac")
	}
	gotMAC := resp[p : p+macLen]
	p += macLen
	origID := binary.BigEndian.Uint16(resp[p:])
	tsigErr := binary.BigEndian.Uint16(resp[p+2:])
	otherLen := int(binary.BigEndian.Uint16(resp[p+4:]))
	p += 6
	if p+otherLen > len(resp) {
		return errors.New("truncated tsig other data")
	}
	other := resp[p : p+otherLen]
	if tsigErr != 0 {
		return fmt.Errorf("tsig error %d", tsigErr)
	}

	stripped := append([]byte(nil), resp[:tsigStart]...)
	binary.BigEndian.PutUint16(stripped[0:2], origID)
	binary.BigEndian.PutUint16(stripped[10:12], uint16(counts[3]-1))
	vars, err := k.variables(time.Unix(int64(signed), 0), fudge, tsigErr, other)
	if err != nil {
		return err
	}
	prefix := binary.BigEndian.AppendUint16(nil, uint16(len(requestMAC)))
	prefix = append(prefix, requestMAC...)
	if !hmac.Equal(gotMAC, k.mac(prefix, stripped, vars)) {
		return errors.New("bad response signature")
	}
	skew := now.Unix() - int64(signed)
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(fudge) {
		return errors.New("response signature is outside the time window")
	}
	return nil
}
