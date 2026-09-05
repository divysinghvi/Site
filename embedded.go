// Package site is the module root. It embeds content/ so the single binary is
// self-contained wherever the source tree is absent at runtime (Vercel ships
// only the compiled function). Local runs keep using the directory on disk.
package site

import (
	"embed"
	"io/fs"
)

//go:embed all:content
var contentTree embed.FS

// ContentFS returns the embedded content/ tree rooted at content/.
func ContentFS() fs.FS {
	sub, err := fs.Sub(contentTree, "content")
	if err != nil {
		return nil
	}
	return sub
}
