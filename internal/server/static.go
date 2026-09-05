package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"divy.dev/internal/web"
)

// staticHandler serves the embedded SvelteKit build (internal/web/dist):
// prerendered pages (<route>.html or <route>/index.html), hashed assets with
// immutable caching, the 200.html SPA fallback for unknown non-API paths and
// 404.html otherwise.
type staticHandler struct {
	s     *Server
	fsys  fs.FS
	etags map[string]string
	index bool
}

func newStaticHandler(s *Server) (*staticHandler, error) {
	h := &staticHandler{s: s, fsys: web.FS(), etags: map[string]string{}}
	if h.fsys == nil {
		return h, nil
	}
	err := fs.WalkDir(h.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == ".gitkeep" {
			return err
		}
		b, err := fs.ReadFile(h.fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		h.etags[p] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	if err != nil {
		return nil, err
	}
	_, h.index = h.etags["index.html"]
	return h, nil
}

func (h *staticHandler) hasIndex() bool { return h.index }

func (h *staticHandler) exists(p string) bool {
	_, ok := h.etags[p]
	return ok
}

func (h *staticHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	h.serveFile(w, r, "index.html", http.StatusOK)
}

func (h *staticHandler) cacheControlFor(p string) string {
	switch {
	case strings.HasPrefix(p, "_app/immutable/"), strings.HasPrefix(p, "fonts/"):
		return CacheIMM
	case strings.HasSuffix(p, ".html"), strings.HasSuffix(p, "__data.json"):
		return CacheHTML
	default:
		return CacheA3600
	}
}

func (h *staticHandler) serveFile(w http.ResponseWriter, r *http.Request, p string, status int) {
	f, err := h.fsys.Open(p)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error: "+RequestID(r.Context()))
		return
	}
	hdr := w.Header()
	ct := mime.TypeByExtension(path.Ext(p))
	if ct == "" {
		ct = http.DetectContentType(b)
	}
	hdr.Set("Content-Type", ct)
	hdr.Set("Cache-Control", h.cacheControlFor(p))
	if status == http.StatusOK {
		if et := h.etags[p]; et != "" {
			hdr.Set("ETag", et)
			if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, et) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	} else {
		hdr.Set("Cache-Control", CacheNS)
	}
	hdr.Set("Content-Length", fmtInt(len(b)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(b)
	}
}

// ServeHTTP is the router's NotFound handler.
func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	up := r.URL.Path
	clean := path.Clean("/" + up)
	rel := strings.TrimPrefix(clean, "/")
	if h.fsys == nil || len(h.etags) == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if rel == "" || rel == "." {
		h.s.handleIndex(w, r)
		return
	}
	// a trailing slash on a page path redirects to the slash-less form (trailingSlash: never)
	if strings.HasSuffix(up, "/") && up != "/" {
		if h.exists(rel+".html") || h.exists(rel+"/index.html") {
			http.Redirect(w, r, clean, http.StatusPermanentRedirect)
			return
		}
	}
	switch {
	case h.exists(rel):
		h.serveFile(w, r, rel, http.StatusOK)
	case h.exists(rel + ".html"):
		h.serveFile(w, r, rel+".html", http.StatusOK)
	case h.exists(rel + "/index.html"):
		h.serveFile(w, r, rel+"/index.html", http.StatusOK)
	case h.exists("200.html"):
		h.serveFile(w, r, "200.html", http.StatusOK)
	case h.exists("404.html"):
		h.serveFile(w, r, "404.html", http.StatusNotFound)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}
