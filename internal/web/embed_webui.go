//go:build webui

package web

import (
	"embed"
	"io/fs"
)

// dist is the dashboard export copied here by the release build (cp -r fe/out/. internal/web/dist).
//
//go:embed all:dist
var dist embed.FS

func init() {
	if sub, err := fs.Sub(dist, "dist"); err == nil {
		embedded = sub
	}
}
