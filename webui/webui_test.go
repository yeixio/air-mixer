package webui

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/yeixio/air-mixer/i18n"
)

func get(t *testing.T, path, acceptLanguage string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if acceptLanguage != "" {
		req.Header.Set("Accept-Language", acceptLanguage)
	}
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)
	return rec
}

func TestPageLanguage(t *testing.T) {
	for header, want := range map[string]string{
		"":                          `<html lang="en" dir="ltr"`,
		"de-AT,de;q=0.9":            `<html lang="de" dir="ltr"`,
		"ar-EG":                     `<html lang="ar" dir="rtl"`,
		"zh-TW":                     `<html lang="zh-Hant" dir="ltr"`,
		"xx, en;q=0.1":              `<html lang="en" dir="ltr"`,
		`"><script>alert(1)</sc`:    `<html lang="en" dir="ltr"`,
		"he;q=0.5, fa;q=0.9, *;q=1": `<html lang="fa" dir="rtl"`,
	} {
		rec := get(t, "/", header)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("Accept-Language %q: status %d, want %s", header, rec.Code, want)
		}
		if lang := strings.Split(strings.TrimPrefix(want, `<html lang="`), `"`)[0]; rec.Header().Get("Content-Language") != lang {
			t.Errorf("Accept-Language %q: Content-Language %q, want %q", header, rec.Header().Get("Content-Language"), lang)
		}
	}
	if !strings.Contains(string(index), string(htmlTag)) {
		t.Fatalf("index.html must start its root element with %s", htmlTag)
	}
}

func TestCatalogServed(t *testing.T) {
	rec := get(t, "/i18n/languages.json", "")
	var langs []i18n.Language
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &langs) != nil || len(langs) != len(i18n.Languages()) {
		t.Fatalf("languages.json: %d %s", rec.Code, rec.Body.String())
	}
	for _, l := range langs {
		for _, ns := range []string{"web", "errors"} {
			rec := get(t, "/i18n/locales/"+l.Code+"/"+ns+".json", "")
			var v map[string]any
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
				t.Errorf("%s/%s.json: %d", l.Code, ns, rec.Code)
			}
		}
	}
	for _, path := range []string{"/i18n/", "/i18n/locales/", "/i18n/locales/de", "/i18n/locales/xx/web.json", "/i18n/../webui.go", "/i18n/i18n.go"} {
		if rec := get(t, path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
	}
}

// Every key the page names, in data-i18n attributes or t('...') calls,
// must be in the English catalog, so nobody sees a raw key.
func TestKeysExist(t *testing.T) {
	en := map[string]map[string]bool{}
	for _, ns := range []string{"web", "errors"} {
		raw, err := fs.ReadFile(i18n.Files(), "locales/en/"+ns+".json")
		if err != nil {
			t.Fatal(err)
		}
		var tree map[string]any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatal(err)
		}
		en[ns] = map[string]bool{}
		var walk func(string, any)
		walk = func(prefix string, v any) {
			switch v := v.(type) {
			case string:
				en[ns][regexp.MustCompile(`_(zero|one|two|few|many|other)$`).ReplaceAllString(prefix, "")] = true
			case map[string]any:
				for k, child := range v {
					if prefix != "" {
						k = prefix + "." + k
					}
					walk(k, child)
				}
			}
		}
		walk("", tree)
	}

	page := string(index)
	uses := regexp.MustCompile(`data-i18n(?:-placeholder|-title|-aria-label)?="([^"]+)"|\bt\(\s*'([^']+)'`).FindAllStringSubmatch(page, -1)
	if len(uses) < 50 {
		t.Fatalf("found only %d keys in index.html", len(uses))
	}
	for _, m := range uses {
		key := m[1] + m[2]
		ns := "web"
		if before, after, ok := strings.Cut(key, ":"); ok {
			ns, key = before, after
		}
		if !en[ns][key] {
			t.Errorf("index.html uses %s:%s, which English doesn't have", ns, key)
		}
	}
}

// Every font the page names is embedded and served; nothing else is.
func TestFontsServed(t *testing.T) {
	uses := regexp.MustCompile(`/fonts/([\w-]+\.woff2)`).FindAllStringSubmatch(string(index), -1)
	if len(uses) == 0 {
		t.Fatal("index.html names no fonts")
	}
	for _, m := range uses {
		rec := get(t, m[0], "")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "font/woff2" || rec.Body.Len() == 0 {
			t.Errorf("%s: status %d, type %q", m[0], rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	for _, path := range []string{"/fonts/", "/fonts/LICENSE-Inter.txt", "/fonts/missing.woff2", "/fonts/../webui.go"} {
		if rec := get(t, path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
	}
}
