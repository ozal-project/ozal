package pipeline

import (
	"strings"
	"testing"
)

func TestParseVisitorIdentity(t *testing.T) {
	long := strings.Repeat("a", maxVisitorField)
	for name, tc := range map[string]struct {
		users, emails []string
		want          Visitor
		ok            bool
	}{
		"subject and email":         {[]string{"sub-1"}, []string{"ana@example.com"}, Visitor{"sub-1", "ana@example.com"}, true},
		"subject only":              {[]string{"sub-1"}, nil, Visitor{Subject: "sub-1"}, true},
		"longest accepted":          {[]string{long}, []string{long}, Visitor{long, long}, true},
		"anonymous":                 {nil, nil, Visitor{}, false},
		"email without subject":     {nil, []string{"ana@example.com"}, Visitor{}, false},
		"two subjects":              {[]string{"sub-1", "sub-2"}, nil, Visitor{}, false},
		"two emails":                {[]string{"sub-1"}, []string{"a@example.com", "b@example.com"}, Visitor{}, false},
		"empty subject":             {[]string{""}, nil, Visitor{}, false},
		"empty email":               {[]string{"sub-1"}, []string{""}, Visitor{}, false},
		"too long subject":          {[]string{long + "a"}, nil, Visitor{}, false},
		"too long email":            {[]string{"sub-1"}, []string{long + "a"}, Visitor{}, false},
		"surrounding spaces":        {[]string{" sub-1"}, nil, Visitor{}, false},
		"control character":         {[]string{"sub-1\x00"}, nil, Visitor{}, false},
		"DEL in email":              {[]string{"sub-1"}, []string{"ana\x7f@example.com"}, Visitor{}, false},
		"newline smuggled in email": {[]string{"sub-1"}, []string{"ana@example.com\nX-Admin: 1"}, Visitor{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := parseVisitorIdentity(tc.users, tc.emails)
			if got != tc.want || ok != tc.ok {
				t.Errorf("parseVisitorIdentity(%q, %q) = %+v, %v; want %+v, %v", tc.users, tc.emails, got, ok, tc.want, tc.ok)
			}
		})
	}
}
