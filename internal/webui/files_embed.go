//go:build webui

package webui

import (
	"embed"
	"io/fs"
)

// dist is the built frontend; `make build` (or `npm run build` in web/) writes it here.
// "all:" also includes files whose names start with "." or "_", which Vite may emit.
//
//go:embed all:dist
var dist embed.FS

// Files returns the built frontend's files, rooted at dist.
func Files() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // can't happen: "dist" is a valid path
	}
	return sub
}
