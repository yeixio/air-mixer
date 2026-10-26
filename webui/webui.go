// Package webui embeds the browser interface.
package webui

import (
	_ "embed"
	"net/http"
)

//go:embed index.html
var index []byte

// Handler serves the single-page web UI for every path.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
