package server

import (
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"divy.dev/internal/promql"
	"divy.dev/internal/trace"
)

// Response-cache bounds (contract §K.3.2).
const (
	cacheMaxBody        = 4 << 20
	cacheDefaultEntries = 2000
	cacheDefaultBytes   = 32 << 20
	cacheTTLQ15         = 15 * time.Second
	cacheTTLC60         = 60 * time.Second
	postBodyLimit       = 1 << 20
)

// responseCache is the in-memory LRU behind the query (Q15, 15 s) and
// content (C60, 60 s) classes. The key is method + path + canonical query
// (+ the form body of a POST); Q15 entries also carry store.Generation()
// and every entry is dropped after a collector run writes (Invalidate).
// Only Content-Type, Cache-Control, ETag and the body are stored; every
// per-request header (trace ids, CORS, Vary, X-Cache) is set outside.
type responseCache struct {
	s          *Server
	maxEntries int
	maxBytes   int64
	now        func() time.Time

	mu    sync.Mutex
	ll    *list.List
	items map[string]*list.Element
	bytes int64
}

type cacheEntry struct {
	key         string
	class       string
	gen         string
	body        []byte
	contentType string
	etag        string
	expires     time.Time
}

func newResponseCache(s *Server, maxEntries int, maxBytes int64) *responseCache {
	if maxEntries <= 0 {
		maxEntries = cacheDefaultEntries
	}
	if maxBytes <= 0 {
		maxBytes = cacheDefaultBytes
	}
	return &responseCache{s: s, maxEntries: maxEntries, maxBytes: maxBytes, now: time.Now, ll: list.New(), items: map[string]*list.Element{}}
}

// Invalidate drops every entry (after a collector round wrote samples or probes).
func (c *responseCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.items = map[string]*list.Element{}
	c.bytes = 0
}

// Len reports the number of entries (tests).
func (c *responseCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *responseCache) gen(class string) string {
	if class == CacheC60 {
		return c.s.cfg.Content.Hash
	}
	if c.s.cfg.Store != nil {
		return strconv.FormatUint(c.s.cfg.Store.Generation(), 10)
	}
	return "0"
}

func (c *responseCache) get(key string) (*cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*cacheEntry)
	if c.now().After(e.expires) || e.gen != c.gen(e.class) {
		c.removeLocked(el)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return e, true
}

func (c *responseCache) put(e *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[e.key]; ok {
		c.removeLocked(el)
	}
	el := c.ll.PushFront(e)
	c.items[e.key] = el
	c.bytes += int64(len(e.body))
	for (c.ll.Len() > c.maxEntries || c.bytes > c.maxBytes) && c.ll.Len() > 1 {
		c.removeLocked(c.ll.Back())
	}
}

func (c *responseCache) removeLocked(el *list.Element) {
	e := el.Value.(*cacheEntry)
	c.ll.Remove(el)
	delete(c.items, e.key)
	c.bytes -= int64(len(e.body))
}

// cacheablePath: only the API families, the ASCII trace and `/` go through
// the cache; the handler's Cache-Control class decides what is stored.
func cacheablePath(p string) bool {
	if p == "/api/collect" {
		return false
	}
	return strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/loki/") || p == "/ascii" || p == "/"
}

// canonicalQuery sorts parameters by name (repeated names keep their order)
// and, on the Prometheus family, rewrites time/start/end and
// step/timeout/lookback_delta to integer milliseconds so equivalent
// spellings share one entry (contract §K.3.2).
func canonicalQuery(path string, v url.Values) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	prom := strings.HasPrefix(path, "/api/v1/")
	var sb strings.Builder
	for _, k := range keys {
		for _, val := range v[k] {
			if prom {
				switch k {
				case "time", "start", "end":
					if t, err := parseTime(val); err == nil {
						val = strconv.FormatInt(t.UnixMilli(), 10)
					}
				case "step", "timeout", "lookback_delta":
					if d, err := promql.ParseAPIDuration(val); err == nil {
						val = strconv.FormatInt(d.Milliseconds(), 10)
					}
				}
			}
			if sb.Len() > 0 {
				sb.WriteByte('&')
			}
			sb.WriteString(url.QueryEscape(k))
			sb.WriteByte('=')
			sb.WriteString(url.QueryEscape(val))
		}
	}
	return sb.String()
}

// cacheKey builds the entry key; ok is false when the request cannot be
// cached (an over-long or non-form POST body).
func cacheKey(r *http.Request) (string, bool) {
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	values := url.Values{}
	for k, vs := range r.URL.Query() {
		values[k] = append(values[k], vs...)
	}
	if r.Method == http.MethodPost {
		ct := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
		if ct != "" && ct != "application/x-www-form-urlencoded" {
			return "", false
		}
		if r.Body != nil {
			body, err := io.ReadAll(io.LimitReader(r.Body, postBodyLimit+1))
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			if err != nil || len(body) > postBodyLimit {
				return "", false
			}
			form, err := url.ParseQuery(string(body))
			if err != nil {
				return "", false
			}
			for k, vs := range form {
				values[k] = append(values[k], vs...)
			}
		}
	}
	key := method + "\n" + r.URL.Path + "\n" + canonicalQuery(r.URL.Path, values)
	if r.URL.Path == "/" {
		if r.URL.Query().Get("format") == "ascii" || wantsText(r.Header.Get("Accept")) {
			key += "\ntext"
		} else {
			key += "\nhtml"
		}
	}
	return key, true
}

// cacheRecorder buffers the handler's status and body while sharing the
// real header map, so the cache can decide afterwards what to store.
type cacheRecorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (c *cacheRecorder) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
}

func (c *cacheRecorder) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.buf.Write(b)
}

// Flush is swallowed: the body is written once the handler returns.
func (c *cacheRecorder) Flush() {}

func weakETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:8]) + `"`
}

// etagMatches implements If-None-Match for one entity tag (weak comparison).
func etagMatches(inm, etag string) bool {
	if inm == "" || etag == "" {
		return false
	}
	strip := func(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "W/") }
	want := strip(etag)
	for _, part := range strings.Split(inm, ",") {
		if part = strings.TrimSpace(part); part == "*" || strip(part) == want {
			return true
		}
	}
	return false
}

// middleware serves hits and records misses (X-Cache: HIT | MISS, both
// answering 304 to a matching If-None-Match).
func (c *responseCache) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !cacheablePath(r.URL.Path) || (r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost) {
			next.ServeHTTP(w, r)
			return
		}
		key, ok := cacheKey(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		inm := r.Header.Get("If-None-Match")
		h := w.Header()
		if e, hit := c.get(key); hit {
			h.Set("Content-Type", e.contentType)
			h.Set("Cache-Control", e.class)
			h.Set("ETag", e.etag)
			h.Set("X-Cache", "HIT")
			if r.URL.Path == "/" {
				h.Add("Vary", "Accept")
			}
			trace.SetAttributes(r.Context(), trace.AttrCache.String("HIT"))
			if etagMatches(inm, e.etag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			h.Set("Content-Length", strconv.Itoa(len(e.body)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(e.body)
			return
		}
		// A miss always runs the handler for the full body (so the entry can
		// be stored); the conditional request is answered here instead.
		if inm != "" {
			r.Header.Del("If-None-Match")
		}
		rec := &cacheRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		body := rec.buf.Bytes()
		class := h.Get("Cache-Control")
		if rec.status == http.StatusOK && (class == CacheQ15 || class == CacheC60) && len(body) <= cacheMaxBody {
			etag := h.Get("ETag")
			if etag == "" {
				etag = weakETag(body)
				h.Set("ETag", etag)
			}
			ttl := cacheTTLC60
			if class == CacheQ15 {
				ttl = cacheTTLQ15
			}
			c.put(&cacheEntry{key: key, class: class, gen: c.gen(class), body: append([]byte(nil), body...), contentType: h.Get("Content-Type"), etag: etag, expires: c.now().Add(ttl)})
			h.Set("X-Cache", "MISS")
			trace.SetAttributes(r.Context(), trace.AttrCache.String("MISS"))
			if etagMatches(inm, etag) {
				h.Del("Content-Length")
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		if rec.status == http.StatusNotModified || rec.status == http.StatusNoContent {
			h.Del("Content-Length")
			w.WriteHeader(rec.status)
			return
		}
		h.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(rec.status)
		_, _ = w.Write(body)
	})
}
