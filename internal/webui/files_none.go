//go:build !webui

package webui

import "io/fs"

// Files returns nil: this binary was built without the web UI (no -tags webui).
func Files() fs.FS { return nil }
