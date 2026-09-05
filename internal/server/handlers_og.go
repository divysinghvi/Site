package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"divy.dev/internal/og"
)

var postmortemIDRe = regexp.MustCompile(`^INC-[0-9]{3}$`)

// ogCache renders each OG image once per process (content is immutable per
// process, so the key is the id; the content hash is part of the ETag).
type ogCache struct {
	s  *Server
	mu sync.Mutex
	m  map[string]ogImage
}

type ogImage struct {
	png  []byte
	etag string
}

func newOGCache(s *Server) *ogCache { return &ogCache{s: s, m: map[string]ogImage{}} }

func (c *ogCache) host() string {
	if u, err := url.Parse(c.s.cfg.SiteOrigin); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(c.s.cfg.SiteOrigin, "https://"), "http://")
}

// get returns the rendered image for key ("default" or a postmortem id),
// rendering it on first use.
func (c *ogCache) get(key string, render func() ([]byte, error)) (ogImage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if img, ok := c.m[key]; ok {
		return img, nil
	}
	png, err := render()
	if err != nil {
		return ogImage{}, err
	}
	sum := sha256.Sum256(append([]byte(c.s.cfg.Content.Hash+"\n"), png...))
	img := ogImage{png: png, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
	c.m[key] = img
	return img, nil
}

func (s *Server) serveOG(w http.ResponseWriter, r *http.Request, key string, render func() ([]byte, error)) {
	img, err := s.og.get(key, render)
	if err != nil {
		s.log.Error("og render", "key", key, "err", err.Error(), "req", RequestID(r.Context()))
		writeError(w, http.StatusInternalServerError, "internal error: "+RequestID(r.Context()))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Cache-Control", CacheA3600)
	h.Set("ETag", img.etag)
	if etagMatches(r.Header.Get("If-None-Match"), img.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(img.png)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(img.png)
	}
}

// handleOGDefault serves /og/default.png: profile name, the tagline as
// written in profile.yaml (a TODO stays a TODO), the site host and the
// service palette.
func (s *Server) handleOGDefault(w http.ResponseWriter, r *http.Request) {
	s.serveOG(w, r, "default", func() ([]byte, error) {
		c := s.cfg.Content
		colors := make([]string, 0, len(c.Spans.Services))
		for _, svc := range c.Spans.Services {
			colors = append(colors, svc.Color)
		}
		return og.RenderDefault(og.Default{Name: c.Profile.Name, Tagline: c.Profile.Tagline, Host: s.og.host(), Colors: colors})
	})
}

// handleOGPostmortem serves /og/postmortems/{id}.png (and the /og/{id}.png alias).
func (s *Server) handleOGPostmortem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !postmortemIDRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "postmortem not found")
		return
	}
	pm, ok := s.cfg.Content.Postmortem(id)
	if !ok {
		writeError(w, http.StatusNotFound, "postmortem not found")
		return
	}
	s.serveOG(w, r, id, func() ([]byte, error) {
		f := pm.Front
		in := og.Postmortem{ID: f.ID, Title: f.Title, Severity: string(f.Severity), Date: string(f.Date), Duration: f.Duration, Summary: f.Summary, Host: s.og.host()}
		for _, name := range f.Services {
			svc := og.Service{Name: name}
			if m, ok := s.cfg.Content.Service(name); ok {
				svc.Color = m.Color
				if m.Title != "" {
					svc.Name = m.Title
				}
			}
			in.Services = append(in.Services, svc)
		}
		return og.RenderPostmortem(in)
	})
}
