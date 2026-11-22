// Package web embeds the built UI. `make build` fills dist/ with the Vite
// build before compiling the binary; without it, fh serves a placeholder.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist is the built UI, rooted at dist/.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist/ is embedded at compile time, so this cannot fail
	}
	return sub
}
