package ext

import (
	"embed"
	"net/http"
	"path"
)

// docs holds the OpenAPI description and a vendored Swagger UI, so the docs also work offline.
//
//go:embed docs/openapi.yaml docs/index.html docs/init.js docs/swagger
var docs embed.FS

var docTypes = map[string]string{
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".png":  "image/png",
	".html": "text/html; charset=utf-8",
}

func serveDoc(w http.ResponseWriter, name, contentType string) {
	body, err := docs.ReadFile(name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "no such file")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(body)
}

// openAPI serves the OpenAPI 3 description (no token needed).
func (a *API) openAPI(w http.ResponseWriter, _ *http.Request) {
	serveDoc(w, "docs/openapi.yaml", "application/yaml; charset=utf-8")
}

// docsPage serves Swagger UI.
func (a *API) docsPage(w http.ResponseWriter, _ *http.Request) {
	serveDoc(w, "docs/index.html", docTypes[".html"])
}

// docsAsset serves the Swagger UI files.
func (a *API) docsAsset(w http.ResponseWriter, r *http.Request) {
	file := path.Base(r.PathValue("file"))
	name := "docs/swagger/" + file
	if file == "init.js" {
		name = "docs/init.js"
	}
	contentType, ok := docTypes[path.Ext(file)]
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such file")
		return
	}
	serveDoc(w, name, contentType)
}
