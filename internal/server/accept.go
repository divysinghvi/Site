package server

import (
	"strconv"
	"strings"
)

// wantsText decides the `/` negotiation (LogQL §L.6.1): text iff q_plain > 0
// and (q_html == 0 or q_plain > q_html); ties → HTML. q is taken from the most
// specific matching media range (text/plain > text/* > */*).
func wantsText(accept string) bool {
	if strings.TrimSpace(accept) == "" {
		return false
	}
	qPlain, qHTML := -1.0, -1.0
	specPlain, specHTML := -1, -1
	for _, part := range strings.Split(accept, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		mr := strings.ToLower(strings.TrimSpace(fields[0]))
		q := 1.0
		for _, p := range fields[1:] {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "q=") {
				if v, err := strconv.ParseFloat(strings.TrimPrefix(p, "q="), 64); err == nil && v >= 0 && v <= 1 {
					q = v
				}
			}
		}
		spec := -1
		switch mr {
		case "*/*":
			spec = 0
		case "text/*":
			spec = 1
		case "text/plain":
			spec = 2
		case "text/html":
			spec = 2
		}
		if spec < 0 {
			continue
		}
		if mr != "text/html" && spec > specPlain {
			specPlain, qPlain = spec, q
		}
		if mr != "text/plain" && spec > specHTML {
			specHTML, qHTML = spec, q
		}
	}
	if qPlain <= 0 {
		return false
	}
	if qHTML <= 0 {
		return true
	}
	return qPlain > qHTML
}
