package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// page is the HTML fh serves when it can't serve the app itself.
func page(message string) []byte {
	return []byte(`<!doctype html>
<html><head><meta charset="utf-8"><title>fullharness</title></head>
<body style="font-family:system-ui;max-width:32rem;margin:4rem auto;padding:0 1rem">
<h1>fullharness</h1>
<p>` + message + `</p>
</body></html>
`)
}

var (
	unauthorisedPage = page("fh restarted, or this tab was opened by an older launch. Run <code>fh</code> to open it again.")
	notBuiltPage     = page("The UI is not built into this binary. Run <code>make build</code>, or <code>make dev</code> for the Vite dev server.")
)

func writeUnauthorised(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write(unauthorisedPage) // a write error means the client went away
}

// uiHandler serves the built UI. Paths that aren't files get index.html, so
// client-side routes survive a reload.
func uiHandler(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if info, err := fs.Stat(ui, name); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(ui, "index.html")
		if err != nil {
			index = notBuiltPage
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index) // a write error means the client went away
	})
}
