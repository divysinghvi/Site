// Package web embeds the SvelteKit build output (internal/web/dist). Only
// dist/.gitkeep is committed; `make web-build` fills the directory before the
// final `go build`. Without an index.html the server serves the JSON hint on
// `/` and every other page path is a 404.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS // "all:" keeps SvelteKit's _app directory (embed skips _-prefixed names by default)

// FS returns the embedded site rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	return sub
}

// HasIndex reports whether a built site (index.html) is embedded.
func HasIndex() bool {
	f := FS()
	if f == nil {
		return false
	}
	st, err := fs.Stat(f, "index.html")
	return err == nil && !st.IsDir()
}
