//go:build webui

package webui_test

// Runs only with -tags webui, after the frontend is built (CI's Web job does both): it
// checks the real build that ends up in the binary, not a stand-in.

import (
	"io/fs"
	"regexp"
	"testing"

	"github.com/arjunshastry16/jobtrack/internal/webui"
)

var (
	scriptTag   = regexp.MustCompile(`<script\b[^>]*>`)
	srcOrHref   = regexp.MustCompile(`(?:src|href)="/([^"]+)"`)
	inlineStyle = regexp.MustCompile(`<style\b|\sstyle="`)
)

func TestEmbeddedBuild(t *testing.T) {
	files := webui.Files()
	if files == nil {
		t.Fatal("Files() is nil with -tags webui")
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		t.Fatalf("index.html not embedded: %v (build the frontend first)", err)
	}

	// Everything index.html loads must be embedded too.
	refs := srcOrHref.FindAllStringSubmatch(string(index), -1)
	if len(refs) == 0 {
		t.Fatalf("index.html loads nothing: %s", index)
	}
	for _, m := range refs {
		if _, err := fs.Stat(files, m[1]); err != nil {
			t.Errorf("index.html loads /%s, which isn't embedded", m[1])
		}
	}

	// The Content-Security-Policy allows no inline scripts or styles, so the build must
	// not contain any, or the app would be blocked in the browser.
	for _, tag := range scriptTag.FindAllString(string(index), -1) {
		if !regexp.MustCompile(`\ssrc="`).MatchString(tag) {
			t.Errorf("inline script in index.html: %s", tag)
		}
	}
	if inlineStyle.Match(index) {
		t.Error("inline style in index.html")
	}
}
