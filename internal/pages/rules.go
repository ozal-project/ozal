package pages

import (
	"bufio"
	"bytes"
	"fmt"
	"net/textproto"
	"strconv"
	"strings"
)

// Rules are a deployment's _redirects and _headers files, parsed (PAGES-003).
// The syntax is a subset of Cloudflare Pages':
//
//	_redirects   one rule per line: SOURCE DESTINATION [STATUS]
//	             /old /new 301
//	             /blog/* /posts/:splat
//	             /u/:name https://example.com/people/:name 302
//
//	_headers     a path pattern, then indented "Name: value" lines;
//	             "! Name" removes a header an earlier rule added
//	             /*
//	               X-Frame-Options: DENY
//	             /embed/*
//	               ! X-Frame-Options
//
// A pattern is a path starting with "/". A segment ":name" matches one
// non-empty segment; a final "*" matches the rest of the path (":splat").
// Redirects take the first matching rule; headers apply every matching rule
// in file order. Out of the subset, and refused: status 200 rewrites and 404
// pages, absolute-URL patterns, placeholders in header values.
//
// A nil *Rules has no rules. Parsing is pure; the files are read by the shell.
type Rules struct {
	redirects []redirect
	headers   []headerRule
}

// RulesError is an invalid rules file, with the line that failed.
type RulesError struct {
	File   string
	Line   int
	Reason string
}

func (e *RulesError) Error() string { return fmt.Sprintf("%s line %d: %s", e.File, e.Line, e.Reason) }

// Rule-file limits, close to Cloudflare Pages': both files are read on every
// deploy and matched on every request, so they stay small.
const (
	MaxRulesFileBytes   = 256 << 10
	maxRuleLineBytes    = 1000
	maxRedirects        = 2100
	maxDynamicRedirects = 100
	maxHeaderRules      = 100
)

// Header is one response header a rule sets.
type Header struct {
	Name  string
	Value string
}

type redirect struct {
	from   pattern
	to     string
	status int
}

type headerRule struct {
	line   int // of the pattern, for errors
	from   pattern
	set    []Header
	detach []string
}

// ParseRules parses both files; either may be nil (absent).
func ParseRules(redirects, headers []byte) (*Rules, error) {
	r := &Rules{}
	var err error
	if r.redirects, err = parseRedirects(redirects); err != nil {
		return nil, err
	}
	if r.headers, err = parseHeaders(headers); err != nil {
		return nil, err
	}
	return r, nil
}

// Redirect returns where a request path is sent, if a rule matches. A
// relative destination that would turn into "//host" or "/\host" after
// substitution (a protocol-relative open redirect built from the visitor's
// own path) is not followed.
func (r *Rules) Redirect(urlPath string) (to string, status int, ok bool) {
	if r == nil {
		return "", 0, false
	}
	for _, rule := range r.redirects {
		params, matched := rule.from.match(urlPath)
		if !matched {
			continue
		}
		to = substitute(rule.to, params)
		if strings.HasPrefix(rule.to, "/") && (strings.HasPrefix(to, "//") || strings.HasPrefix(to, "/\\")) {
			return "", 0, false
		}
		if strings.ContainsFunc(to, isControl) {
			return "", 0, false
		}
		return to, rule.status, true
	}
	return "", 0, false
}

// Headers returns the headers for a request path: every matching rule in
// order, a repeated name joined with ", ", a detached name removed.
func (r *Rules) Headers(urlPath string) []Header {
	if r == nil {
		return nil
	}
	var out []Header
	for _, rule := range r.headers {
		if _, ok := rule.from.match(urlPath); !ok {
			continue
		}
		for _, h := range rule.set {
			if i := indexHeader(out, h.Name); i >= 0 {
				out[i].Value += ", " + h.Value
			} else {
				out = append(out, h)
			}
		}
		for _, name := range rule.detach {
			if i := indexHeader(out, name); i >= 0 {
				out = append(out[:i], out[i+1:]...)
			}
		}
	}
	return out
}

func indexHeader(hs []Header, name string) int {
	for i, h := range hs {
		if h.Name == name {
			return i
		}
	}
	return -1
}

func parseRedirects(src []byte) ([]redirect, error) {
	var out []redirect
	dynamic := 0
	err := eachLine("_redirects", src, func(n int, line string) error {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			return nil
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields) > 3 {
			return &RulesError{"_redirects", n, "want SOURCE DESTINATION [STATUS]"}
		}
		from, err := parsePattern(fields[0])
		if err != nil {
			return &RulesError{"_redirects", n, err.Error()}
		}
		status := 301
		if len(fields) == 3 {
			status, err = strconv.Atoi(fields[2])
			if err != nil || !redirectStatus(status) {
				return &RulesError{"_redirects", n, "status must be 301, 302, 303, 307 or 308"}
			}
		}
		if err := checkDestination(fields[1], from); err != nil {
			return &RulesError{"_redirects", n, err.Error()}
		}
		if from.dynamic() {
			dynamic++
		}
		if len(out) == maxRedirects || dynamic > maxDynamicRedirects {
			return &RulesError{"_redirects", n, fmt.Sprintf("more than %d rules or %d with placeholders", maxRedirects, maxDynamicRedirects)}
		}
		out = append(out, redirect{from: from, to: fields[1], status: status})
		return nil
	})
	return out, err
}

func redirectStatus(code int) bool {
	switch code {
	case 301, 302, 303, 307, 308:
		return true
	}
	return false
}

// checkDestination accepts a site path or an absolute http(s) URL. In a URL,
// placeholders may only appear after the host: a visitor's path must never
// choose where the redirect goes.
func checkDestination(to string, from pattern) error {
	if strings.ContainsFunc(to, isControl) || strings.Contains(to, "\\") {
		return fmt.Errorf("destination has a control character or backslash")
	}
	switch {
	case strings.HasPrefix(to, "//"):
		return fmt.Errorf("destination must be a path or an http(s) URL, not protocol-relative")
	case strings.HasPrefix(to, "/"):
	case strings.HasPrefix(to, "https://"), strings.HasPrefix(to, "http://"):
		rest := to[strings.Index(to, "://")+3:]
		host, _, _ := strings.Cut(rest, "/")
		if host == "" {
			return fmt.Errorf("destination URL has no host")
		}
		if strings.Contains(host, "@") {
			return fmt.Errorf("destination URL must not carry credentials")
		}
		if strings.Contains(host, ":splat") || placeholderIn(host, from) {
			return fmt.Errorf("placeholders are allowed only in the destination path")
		}
	default:
		return fmt.Errorf("destination must be a path or an http(s) URL")
	}
	for _, name := range placeholders(to) {
		if name != "splat" && !from.has(name) {
			return fmt.Errorf("destination uses :%s, which the source does not define", name)
		}
		if name == "splat" && !from.splat {
			return fmt.Errorf("destination uses :splat but the source has no *")
		}
	}
	return nil
}

func placeholderIn(s string, from pattern) bool {
	for _, name := range from.names() {
		if strings.Contains(s, ":"+name) {
			return true
		}
	}
	return false
}

// forbiddenHeaders are headers a site may not set: framing and hop-by-hop
// headers belong to the server, a redirect belongs in _redirects, and a
// cookie set by one site under the shared parent domain could plant a
// session cookie for every other host Switchboard serves there.
var forbiddenHeaders = map[string]bool{
	"Connection": true, "Content-Length": true, "Content-Encoding": true,
	"Keep-Alive": true, "Proxy-Authenticate": true, "Proxy-Connection": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
	"Location": true, "Set-Cookie": true, "X-Content-Type-Options": true,
}

func parseHeaders(src []byte) ([]headerRule, error) {
	var out []headerRule
	err := eachLine("_headers", src, func(n int, line string) error {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			return nil
		}
		if line[0] != ' ' && line[0] != '\t' {
			if err := checkNotEmpty(out); err != nil {
				return err
			}
			from, err := parsePattern(trimmed)
			if err != nil {
				return &RulesError{"_headers", n, err.Error()}
			}
			if len(out) == maxHeaderRules {
				return &RulesError{"_headers", n, fmt.Sprintf("more than %d rules", maxHeaderRules)}
			}
			out = append(out, headerRule{line: n, from: from})
			return nil
		}
		if len(out) == 0 {
			return &RulesError{"_headers", n, "header line before any path pattern"}
		}
		rule := &out[len(out)-1]
		if name, ok := strings.CutPrefix(trimmed, "!"); ok {
			name = strings.TrimSpace(name)
			if !validHeaderName(name) {
				return &RulesError{"_headers", n, "invalid header name"}
			}
			rule.detach = append(rule.detach, textproto.CanonicalMIMEHeaderKey(name))
			return nil
		}
		name, value, ok := strings.Cut(trimmed, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || !validHeaderName(name) {
			return &RulesError{"_headers", n, `want "Name: value" or "! Name"`}
		}
		name = textproto.CanonicalMIMEHeaderKey(name)
		if forbiddenHeaders[name] {
			return &RulesError{"_headers", n, name + " cannot be set by a site"}
		}
		if value == "" || strings.ContainsFunc(value, func(r rune) bool { return r != '\t' && isControl(r) }) {
			return &RulesError{"_headers", n, "header value is empty or has a control character"}
		}
		rule.set = append(rule.set, Header{Name: name, Value: value})
		return nil
	})
	if err == nil {
		err = checkNotEmpty(out)
	}
	return out, err
}

// checkNotEmpty refuses a pattern with no header lines under it: almost
// always a lost indentation, which would otherwise silently do nothing.
func checkNotEmpty(rules []headerRule) error {
	if n := len(rules); n > 0 && len(rules[n-1].set)+len(rules[n-1].detach) == 0 {
		return &RulesError{"_headers", rules[n-1].line, "pattern has no headers (are they indented?)"}
	}
	return nil
}

// validHeaderName reports whether s is an RFC 9110 token.
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0) {
			return false
		}
	}
	return true
}

func eachLine(file string, src []byte, fn func(n int, line string) error) error {
	if len(src) > MaxRulesFileBytes {
		return &RulesError{file, 0, "file is larger than the limit"}
	}
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 4096), maxRuleLineBytes+1)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSuffix(sc.Text(), "\r")
		if len(line) > maxRuleLineBytes {
			return &RulesError{file, n, "line is too long"}
		}
		if err := fn(n, line); err != nil {
			return err
		}
	}
	if sc.Err() != nil {
		return &RulesError{file, n + 1, "line is too long"}
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// pattern is a parsed path pattern: literal or ":name" segments, optionally
// ending in a splat that matches the rest of the path.
type pattern struct {
	segments []string // literal text, or ":name"
	prefix   string   // with splat: literal text before "*" in the last segment
	splat    bool
}

func parsePattern(s string) (pattern, error) {
	if !strings.HasPrefix(s, "/") {
		return pattern{}, fmt.Errorf("pattern must be a path starting with /")
	}
	if strings.ContainsFunc(s, isControl) || strings.Contains(s, "\\") {
		return pattern{}, fmt.Errorf("pattern has a control character or backslash")
	}
	var p pattern
	if i := strings.IndexByte(s, '*'); i >= 0 {
		if i != len(s)-1 {
			return pattern{}, fmt.Errorf("* is allowed only at the end of a pattern")
		}
		p.splat = true
		s = s[:i]
		// "/blog/*" splits into segments "blog" plus an empty splat prefix;
		// "/blog*" keeps "blog" as the literal prefix of the splat.
		cut := strings.LastIndexByte(s, '/')
		p.prefix, s = s[cut+1:], s[:cut]
		if strings.Contains(p.prefix, ":") {
			return pattern{}, fmt.Errorf("a placeholder cannot share a segment with *")
		}
	}
	seen := map[string]bool{}
	var segments []string
	if rest := strings.TrimPrefix(s, "/"); rest != "" {
		segments = strings.Split(rest, "/") // "/" alone has no segments
	}
	for _, seg := range segments {
		if name, ok := strings.CutPrefix(seg, ":"); ok {
			if !validPlaceholder(name) || name == "splat" || seen[name] {
				return pattern{}, fmt.Errorf("invalid or repeated placeholder :%s", name)
			}
			seen[name] = true
		} else if strings.Contains(seg, ":") {
			return pattern{}, fmt.Errorf("a placeholder must be a whole segment")
		}
		p.segments = append(p.segments, seg)
	}
	return p, nil
}

func validPlaceholder(name string) bool {
	if name == "" || !('a' <= name[0] && name[0] <= 'z' || 'A' <= name[0] && name[0] <= 'Z') {
		return false
	}
	for _, c := range []byte(name) {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func (p pattern) dynamic() bool { return p.splat || len(p.names()) > 0 }

func (p pattern) names() []string {
	var out []string
	for _, seg := range p.segments {
		if name, ok := strings.CutPrefix(seg, ":"); ok {
			out = append(out, name)
		}
	}
	return out
}

func (p pattern) has(name string) bool {
	for _, n := range p.names() {
		if n == name {
			return true
		}
	}
	return false
}

// match reports whether urlPath matches and returns the placeholder values.
func (p pattern) match(urlPath string) (map[string]string, bool) {
	if !strings.HasPrefix(urlPath, "/") {
		return nil, false
	}
	rest := urlPath[1:]
	var params map[string]string
	for i, seg := range p.segments {
		var part string
		last := i == len(p.segments)-1 && !p.splat
		if last {
			part, rest = rest, ""
			if strings.Contains(part, "/") {
				return nil, false
			}
		} else {
			var found bool
			part, rest, found = strings.Cut(rest, "/")
			if !found {
				return nil, false
			}
		}
		if name, ok := strings.CutPrefix(seg, ":"); ok {
			if part == "" {
				return nil, false
			}
			if params == nil {
				params = map[string]string{}
			}
			params[name] = part
		} else if part != seg {
			return nil, false
		}
	}
	if !p.splat {
		// "/" has no segments and matches only the root.
		return params, len(p.segments) > 0 || urlPath == "/"
	}
	splat, ok := strings.CutPrefix(rest, p.prefix)
	if !ok {
		return nil, false
	}
	if params == nil {
		params = map[string]string{}
	}
	params["splat"] = splat
	return params, true
}

// placeholders lists the ":name" references in a destination.
func placeholders(to string) []string {
	var out []string
	for i := 0; i < len(to); i++ {
		if to[i] != ':' {
			continue
		}
		j := i + 1
		for j < len(to) && (to[j] == '_' || 'a' <= to[j] && to[j] <= 'z' || 'A' <= to[j] && to[j] <= 'Z' || '0' <= to[j] && to[j] <= '9') {
			j++
		}
		if j > i+1 && validPlaceholder(to[i+1:j]) {
			out = append(out, to[i+1:j])
		}
		i = j - 1
	}
	return out
}

func substitute(to string, params map[string]string) string {
	if len(params) == 0 {
		return to
	}
	var b strings.Builder
	for i := 0; i < len(to); i++ {
		if to[i] == ':' {
			j := i + 1
			for j < len(to) && (to[j] == '_' || 'a' <= to[j] && to[j] <= 'z' || 'A' <= to[j] && to[j] <= 'Z' || '0' <= to[j] && to[j] <= '9') {
				j++
			}
			if v, ok := params[to[i+1:j]]; ok && j > i+1 {
				b.WriteString(v)
				i = j - 1
				continue
			}
		}
		b.WriteByte(to[i])
	}
	return b.String()
}
