// Package webui embeds the browser interface.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/yeixio/air-mixer/i18n"
)

//go:embed index.html
var index []byte

// The typefaces Matinee uses, Inter and Fraunces (SIL Open Font License,
// see fonts/LICENSE-*.txt), for the page to use without reaching the
// internet.
//
//go:embed fonts/*.woff2
var fonts embed.FS

// The page's root element as written; the handler swaps in the visitor's
// language and direction.
var htmlTag = []byte(`<html lang="en" dir="ltr"`)

// Handler serves the single-page web UI for every path, and the text it
// shows in every language under /i18n/.
func Handler() http.Handler {
	catalog := http.StripPrefix("/i18n/", http.FileServerFS(i18n.Files()))
	fontFiles := http.FileServerFS(fonts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name, ok := strings.CutPrefix(r.URL.Path, "/fonts/"); ok {
			serveFont(w, r, fontFiles, name)
			return
		}
		if rest, ok := strings.CutPrefix(r.URL.Path, "/i18n/"); ok {
			serveCatalog(w, r, catalog, rest)
			return
		}
		lang := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
		if lang == "" {
			lang = i18n.Source
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Language", lang)
		w.Header().Add("Vary", "Accept-Language")
		w.Write(Page(lang))
	})
}

// serveCatalog serves languages.json and locales/<tag>/<namespace>.json,
// and nothing else: no directory listings.
func serveCatalog(w http.ResponseWriter, r *http.Request, catalog http.Handler, name string) {
	if !strings.HasSuffix(name, ".json") {
		http.NotFound(w, r)
		return
	}
	if info, err := fs.Stat(i18n.Files(), name); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	catalog.ServeHTTP(w, r)
}

// serveFont serves one of the embedded fonts, which never change within a
// release, so browsers may keep them for a day.
func serveFont(w http.ResponseWriter, r *http.Request, files http.Handler, name string) {
	if !strings.HasSuffix(name, ".woff2") || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	if _, err := fs.Stat(fonts, "fonts/"+name); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Type", "font/woff2")
	files.ServeHTTP(w, r)
}

// Page is the web UI with lang and dir set for a listed language.
func Page(lang string) []byte {
	if lang == i18n.Source {
		return index
	}
	tag := []byte(`<html lang="` + lang + `" dir="` + i18n.Dir(lang) + `"`)
	return bytes.Replace(index, htmlTag, tag, 1)
}
