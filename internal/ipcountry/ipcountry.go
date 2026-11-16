// Package ipcountry answers which country an IP address is registered in,
// from a local copy of a range table such as DB-IP's "IP to Country Lite"
// CSV (start,end,country). Nothing leaves the host: the table is read once
// and every lookup is a binary search in memory.
//
// It is cosmetic context for people (a flag next to an address on the
// enrolment page), never an input to a security decision: IP-to-country data
// is approximate, and mobile carriers and VPNs often place a person elsewhere.
package ipcountry

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync/atomic"
)

// Ranges are stored compactly: IPv4 as integers, the country as two bytes,
// and adjacent ranges of the same country merged, so a full table costs a few
// megabytes instead of tens.
type span4 struct {
	start, end uint32
	country    [2]byte
}

type span6 struct {
	start, end [16]byte
	country    [2]byte
}

// Table is a sorted list of non-overlapping address ranges.
type Table struct {
	v4 []span4
	v6 []span6
}

// maxRows bounds what a table may hold, so a wrong file cannot exhaust memory.
// DB-IP's country table has fewer than a million rows.
const maxRows = 4_000_000

// Parse reads start,end,country rows. Rows whose country is not two ASCII
// letters, or is "ZZ" (DB-IP's unknown), are skipped; a malformed address
// fails the whole table. ctx stops a long parse.
func Parse(ctx context.Context, r io.Reader) (*Table, error) {
	reader := csv.NewReader(bufio.NewReader(r))
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true
	t := &Table{}
	for line := 1; ; line++ {
		if line%65536 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("ipcountry: line %d: %w", line, err)
		}
		if len(record) < 3 {
			return nil, fmt.Errorf("ipcountry: line %d: want start,end,country", line)
		}
		start, err1 := netip.ParseAddr(strings.TrimSpace(record[0]))
		end, err2 := netip.ParseAddr(strings.TrimSpace(record[1]))
		start, end = start.Unmap(), end.Unmap()
		if err1 != nil || err2 != nil || start.Is4() != end.Is4() || end.Less(start) {
			return nil, fmt.Errorf("ipcountry: line %d: invalid range", line)
		}
		code, ok := countryCode(strings.TrimSpace(record[2]))
		if !ok {
			continue
		}
		if len(t.v4)+len(t.v6) >= maxRows {
			return nil, fmt.Errorf("ipcountry: more than %d rows", maxRows)
		}
		if start.Is4() {
			t.v4 = append(t.v4, span4{start: v4(start), end: v4(end), country: code})
		} else {
			t.v6 = append(t.v6, span6{start: start.As16(), end: end.As16(), country: code})
		}
	}
	var err error
	if t.v4, err = merge(t.v4, func(s span4) (uint32, uint32, [2]byte) { return s.start, s.end, s.country },
		func(a, b uint32) int { return compare32(a, b) }, func(a uint32) uint32 { return a + 1 },
		func(s *span4, end uint32) { s.end = end }); err != nil {
		return nil, err
	}
	if t.v6, err = merge(t.v6, func(s span6) ([16]byte, [16]byte, [2]byte) { return s.start, s.end, s.country },
		compare128, next128, func(s *span6, end [16]byte) { s.end = end }); err != nil {
		return nil, err
	}
	return t, nil
}

// merge sorts ranges, refuses overlaps and joins adjacent ranges of one
// country.
func merge[S any, A comparable](spans []S, parts func(S) (A, A, [2]byte), cmp func(a, b A) int, next func(A) A, setEnd func(*S, A)) ([]S, error) {
	slices.SortFunc(spans, func(a, b S) int {
		as, _, _ := parts(a)
		bs, _, _ := parts(b)
		return cmp(as, bs)
	})
	out := spans[:0]
	for _, s := range spans {
		if len(out) > 0 {
			last := &out[len(out)-1]
			_, lastEnd, lastCountry := parts(*last)
			start, end, country := parts(s)
			if cmp(lastEnd, start) >= 0 {
				return nil, fmt.Errorf("ipcountry: ranges overlap")
			}
			if country == lastCountry && next(lastEnd) == start {
				setEnd(last, end)
				continue
			}
		}
		out = append(out, s)
	}
	return slices.Clip(out), nil
}

// Open reads a table from a file, gunzipping it when the name ends in .gz.
func Open(ctx context.Context, path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("ipcountry: %w", err)
		}
		defer gz.Close()
		r = gz
	}
	return Parse(ctx, r)
}

// IsLocal reports a LAN, loopback, link-local or unspecified address: one
// that names a place on the host's own network rather than on the Internet.
func IsLocal(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified()
}

// Country returns the two-letter code for addr, or "" when the table does not
// cover it. Local addresses never have one.
func (t *Table) Country(addr netip.Addr) string {
	addr = addr.Unmap()
	if t == nil || !addr.IsValid() || IsLocal(addr) {
		return ""
	}
	if addr.Is4() {
		a := v4(addr)
		i, found := slices.BinarySearchFunc(t.v4, a, func(s span4, a uint32) int { return compare32(s.start, a) })
		if !found {
			i--
		}
		if i >= 0 && a <= t.v4[i].end {
			return string(t.v4[i].country[:])
		}
		return ""
	}
	a := addr.As16()
	i, found := slices.BinarySearchFunc(t.v6, a, func(s span6, a [16]byte) int { return compare128(s.start, a) })
	if !found {
		i--
	}
	if i >= 0 && compare128(a, t.v6[i].end) <= 0 {
		return string(t.v6[i].country[:])
	}
	return ""
}

// Len is the number of ranges loaded, after merging.
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	return len(t.v4) + len(t.v6)
}

// Async is a table loaded in the background: it answers "" until the load
// finishes, so a large table never delays the listeners.
type Async struct {
	table atomic.Pointer[Table]
}

// LoadAsync starts reading path in a goroutine that ctx cancels. done, if not
// nil, receives the table (nil on error) and the error when the load ends.
func LoadAsync(ctx context.Context, path string, done func(*Table, error)) *Async {
	a := &Async{}
	go func() {
		t, err := Open(ctx, path)
		if err == nil {
			a.table.Store(t)
		}
		if done != nil {
			done(t, err)
		}
	}()
	return a
}

// Country answers from the table once it is loaded.
func (a *Async) Country(addr netip.Addr) string {
	return a.table.Load().Country(addr)
}

func countryCode(s string) ([2]byte, bool) {
	if len(s) != 2 {
		return [2]byte{}, false
	}
	c := [2]byte{upper(s[0]), upper(s[1])}
	if c[0] < 'A' || c[0] > 'Z' || c[1] < 'A' || c[1] > 'Z' || c == [2]byte{'Z', 'Z'} {
		return [2]byte{}, false
	}
	return c, true
}

func upper(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 'a' + 'A'
	}
	return b
}

func v4(a netip.Addr) uint32 {
	b := a.As4()
	return binary.BigEndian.Uint32(b[:])
}

func compare32(a, b uint32) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func compare128(a, b [16]byte) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// next128 is a+1, wrapping at the top of the space (which then never equals
// a later start, so no merge happens across it).
func next128(a [16]byte) [16]byte {
	for i := 15; i >= 0; i-- {
		a[i]++
		if a[i] != 0 {
			break
		}
	}
	return a
}
