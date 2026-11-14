package ipcountry

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `1.0.0.0,1.0.0.255,AU
190.104.128.0,190.104.255.255,PY
8.8.8.0,8.8.8.255,US
10.0.0.0,10.255.255.255,ZZ
2001:db8::,2001:db8:ffff:ffff:ffff:ffff:ffff:ffff,DE
200.0.0.0,200.0.0.255,-
`

func TestCountry(t *testing.T) {
	table, err := Parse(context.Background(), strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if table.Len() != 4 {
		t.Fatalf("loaded %d ranges, want 4 (ZZ and - skipped)", table.Len())
	}
	if !IsLocal(netip.MustParseAddr("192.168.1.1")) || IsLocal(netip.MustParseAddr("190.104.190.61")) {
		t.Error("IsLocal is wrong")
	}
	for _, c := range []struct{ addr, want string }{
		{"190.104.190.61", "PY"}, {"190.104.128.0", "PY"}, {"190.104.255.255", "PY"},
		{"190.105.0.0", ""}, {"8.8.8.8", "US"}, {"1.0.0.0", "AU"}, {"0.255.255.255", ""},
		{"2001:db8::1", "DE"}, {"::ffff:190.104.190.61", "PY"},
		{"192.168.100.5", ""}, {"10.1.2.3", ""}, {"127.0.0.1", ""}, {"fe80::1", ""}, {"200.0.0.1", ""},
	} {
		if got := table.Country(netip.MustParseAddr(c.addr)); got != c.want {
			t.Errorf("Country(%s) = %q, want %q", c.addr, got, c.want)
		}
	}
	var none *Table
	if none.Country(netip.MustParseAddr("8.8.8.8")) != "" || none.Len() != 0 {
		t.Error("a nil table answered")
	}
}

func TestParseRefusesBadTables(t *testing.T) {
	for name, body := range map[string]string{
		"bad address": "1.0.0.x,1.0.0.255,AU\n",
		"reversed":    "1.0.0.255,1.0.0.0,AU\n",
		"mixed":       "1.0.0.0,2001:db8::,AU\n",
		"overlap":     "1.0.0.0,1.0.0.255,AU\n1.0.0.128,1.0.1.0,NZ\n",
		"short":       "1.0.0.0,1.0.0.255\n",
	} {
		if _, err := Parse(context.Background(), strings.NewReader(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOpenReadsGzip(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte(sample))
	gz.Close()
	path := filepath.Join(t.TempDir(), "dbip-country-lite.csv.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	table, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if table.Country(netip.MustParseAddr("190.104.190.61")) != "PY" {
		t.Fatal("gzip table lookup failed")
	}
}

// Adjacent ranges of one country are merged; a lookup still answers inside
// and refuses outside.
func TestAdjacentRangesMerge(t *testing.T) {
	table, err := Parse(context.Background(), strings.NewReader("8.8.8.0,8.8.8.127,US\n8.8.8.128,8.8.8.255,US\n8.8.9.0,8.8.9.255,CA\n"))
	if err != nil {
		t.Fatal(err)
	}
	if table.Len() != 2 || table.Country(netip.MustParseAddr("8.8.8.200")) != "US" || table.Country(netip.MustParseAddr("8.8.9.1")) != "CA" {
		t.Fatalf("len %d", table.Len())
	}
}

// A table that is still loading answers nothing; once loaded it answers.
func TestLoadAsync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "table.csv")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	async := LoadAsync(context.Background(), path, func(_ *Table, err error) { done <- err })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if async.Country(netip.MustParseAddr("8.8.8.8")) != "US" {
		t.Fatal("the loaded table does not answer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	missing := LoadAsync(ctx, filepath.Join(t.TempDir(), "missing.csv"), func(_ *Table, err error) { done <- err })
	if err := <-done; err == nil || missing.Country(netip.MustParseAddr("8.8.8.8")) != "" {
		t.Fatal("a failed load answered")
	}
}

func FuzzParse(f *testing.F) {
	f.Add(sample)
	f.Add("::,::,US\n")
	f.Add("1.1.1.1,1.1.1.1,AR\n1.1.1.2,1.1.1.9,AR\n")
	f.Fuzz(func(t *testing.T, body string) {
		table, err := Parse(context.Background(), strings.NewReader(body))
		if err != nil {
			return
		}
		for _, s := range table.v4 {
			addr := netip.AddrFrom4([4]byte{byte(s.start >> 24), byte(s.start >> 16), byte(s.start >> 8), byte(s.start)})
			if !IsLocal(addr) && table.Country(addr) != string(s.country[:]) {
				t.Fatalf("Country(%s) = %q, want %q", addr, table.Country(addr), s.country)
			}
		}
		for _, s := range table.v6 {
			addr := netip.AddrFrom16(s.end).Unmap()
			if !IsLocal(addr) && addr.Is6() && table.Country(addr) != string(s.country[:]) {
				t.Fatalf("Country(%s) = %q, want %q", addr, table.Country(addr), s.country)
			}
		}
	})
}
