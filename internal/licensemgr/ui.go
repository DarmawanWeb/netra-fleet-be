package licensemgr

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// ui is the web interface: plain HTML, CSS and JavaScript modules, no build step and nothing
// fetched from the internet, so it works offline.
//
//go:embed ui
var ui embed.FS

func uiHandler() http.Handler {
	sub, err := fs.Sub(ui, "ui")
	if err != nil {
		panic(err) // the folder is embedded
	}
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			replyError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") { // no folder listings
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}
