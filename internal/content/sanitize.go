package content

import (
	"regexp"
	"sort"
	"strings"
)

type sanitizePattern struct {
	name  string
	re    *regexp.Regexp
	level Level
	// skipFences skips matches inside markdown code fences.
	skipFences bool
	// minDigits requires at least this many digits in the match (phone shapes).
	minDigits int
	// allowTodo ignores matches whose line holds the TODO marker (emails).
	allowTodo bool
}

var sanitizePatterns = []sanitizePattern{
	{name: "looks like a private key", re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), level: LevelError},
	{name: "looks like a GitHub token", re: regexp.MustCompile(`ghp_[A-Za-z0-9]{36}|github_pat_`), level: LevelError},
	{name: "looks like an AWS access key", re: regexp.MustCompile(`AKIA[0-9A-Z]{16}`), level: LevelError},
	{name: "looks like a Stripe key", re: regexp.MustCompile(`sk_(live|test)_`), level: LevelError},
	{name: "looks like a Slack token", re: regexp.MustCompile(`xox[abp]-`), level: LevelError},
	{name: "looks like a bearer token", re: regexp.MustCompile(`Bearer [A-Za-z0-9._-]{20,}`), level: LevelError},
	{name: "looks like a credential assignment", re: regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key)\s*[=:]\s*\S{8,}`), level: LevelError},
	{name: "looks like an internal hostname", re: regexp.MustCompile(`[a-z0-9-]+\.(internal|local|lan|corp|intranet)\b`), level: LevelError},
	{name: "looks like a gradr.se subdomain", re: regexp.MustCompile(`\b[a-z0-9-]+\.gradr\.se\b`), level: LevelError},
	{name: "looks like an IP address", re: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), level: LevelError},
	{name: "looks like an email address", re: regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.]+`), level: LevelError, allowTodo: true},
	{name: "looks like a phone number", re: regexp.MustCompile(`(?:\+\d{1,3}[ -]?)?(?:\(?\d{2,4}\)?[ -]?)\d{3,4}[ -]?\d{4}\b`), level: LevelWarn, skipFences: true, minDigits: 10},
	{name: "looks like a host-specific name (use generic component names)", re: regexp.MustCompile(`\b[a-z0-9]+-prod-[a-z0-9-]+\b`), level: LevelWarn},
	{name: "looks like an environment variable value (names are fine, values never)", re: regexp.MustCompile(`\b[A-Z_]{4,}=\S+`), level: LevelWarn, skipFences: true},
}

// sanitizeAll runs the sanitizer patterns over every content file (rule pm.sanitize).
func (l *loader) sanitizeAll() {
	files := make([]string, 0, len(l.raws))
	for f := range l.raws {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		l.sanitizeFile(f, string(l.raws[f]))
	}
}

func (l *loader) sanitizeFile(file, text string) {
	inFence := false
	for i, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
			continue
		}
		for _, p := range sanitizePatterns {
			if p.skipFences && inFence {
				continue
			}
			// profile.yaml is the public contact card: its own email/phone are intentional.
			if strings.HasSuffix(file, "profile.yaml") && (p.name == "looks like an email address" || p.name == "looks like a phone number") {
				continue
			}
			for _, m := range p.re.FindAllString(ln, -1) {
				if p.allowTodo && (strings.Contains(m, TodoMarker) || reservedDomain(m)) {
					continue
				}
				if p.minDigits > 0 {
					if countDigits(m) < p.minDigits || strings.Contains(m, "2465") {
						continue
					}
				}
				if p.level == LevelError {
					l.c.Report.errorf(file, i+1, 1, "pm.sanitize", "", "%s: %q", p.name, m)
				} else {
					l.c.Report.warnf(file, i+1, 1, "pm.sanitize", "", "%s: %q", p.name, m)
				}
			}
		}
	}
}

func countDigits(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// reservedDomain reports whether an address uses an RFC 2606 documentation domain.
func reservedDomain(addr string) bool {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return false
	}
	host := strings.ToLower(strings.TrimRight(addr[at+1:], "."))
	for _, d := range []string{"example.com", "example.net", "example.org", "example"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
