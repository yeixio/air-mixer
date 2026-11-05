// Package i18n holds Air-Mixer's text in every language (locales/, served
// to the web UI) and the list of languages people can pick
// (languages.json). The server uses it to pick the language a browser gets
// and to word its error messages. See README.md.
//
// The language list and the matching rules are the same as Matinee's, so
// both show the same language on the same device.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// Source is the language the catalog is written in, and the last fallback.
const Source = "en"

// Language is one of the languages people can pick.
type Language struct {
	Code   string `json:"code"` // BCP 47, such as de, pt-BR or zh-Hant
	Name   string `json:"name"` // in the language itself, such as Deutsch
	Dir    string `json:"dir"`  // ltr or rtl
	Status string `json:"status"`
}

//go:embed languages.json locales
var files embed.FS

// Files holds languages.json and locales/<tag>/<namespace>.json.
func Files() fs.FS { return files }

var languages = func() []Language {
	raw, err := files.ReadFile("languages.json")
	if err != nil {
		panic("i18n: " + err.Error())
	}
	var out []Language
	if err := json.Unmarshal(raw, &out); err != nil {
		panic("i18n: languages.json: " + err.Error())
	}
	return out
}()

// Languages lists the languages people can pick, English first.
func Languages() []Language { return languages }

// Lookup returns a listed language by its exact code.
func Lookup(code string) (Language, bool) {
	for _, l := range languages {
		if l.Code == code {
			return l, true
		}
	}
	return Language{}, false
}

// Dir is the writing direction of a listed language: ltr or rtl.
func Dir(code string) string {
	if l, ok := Lookup(code); ok && l.Dir != "" {
		return l.Dir
	}
	return "ltr"
}

// Old or other names for listed languages: Norwegian is written in Bokmål,
// and iw and in are Hebrew's and Indonesian's former codes.
var aliases = map[string]string{"no": "nb", "nn": "nb", "iw": "he", "in": "id"}

// Chinese regions and the script each writes, for tags such as zh-TW that
// don't name one.
var chineseScripts = map[string]string{"tw": "Hant", "hk": "Hant", "mo": "Hant", "cn": "Hans", "sg": "Hans", "my": "Hans"}

// scriptOf returns a tag's script, such as Hant for zh-Hant-TW or zh-TW, or
// "" when the tag doesn't say.
func scriptOf(parts []string) string {
	for _, p := range parts[1:] {
		if len(p) == 4 {
			return strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
	}
	if parts[0] != "zh" {
		return ""
	}
	for _, p := range parts[1:] {
		if s, ok := chineseScripts[strings.ToLower(p)]; ok {
			return s
		}
	}
	return "Hans"
}

// Match finds the listed language for a tag: exactly (pt-BR), by base
// language (de-AT → de), by script (zh-TW → zh-Hant), or by another region
// of the language (pt-PT → pt-BR, never Simplified for Traditional
// Chinese). It returns "" for none.
func Match(tag string) string {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"), "-")
	if parts[0] == "" {
		return ""
	}
	parts[0] = strings.ToLower(parts[0])
	if a, ok := aliases[parts[0]]; ok {
		parts[0] = a
	}
	whole := strings.ToLower(strings.Join(parts, "-"))
	for _, l := range languages {
		if strings.ToLower(l.Code) == whole {
			return l.Code
		}
	}
	for _, l := range languages {
		if l.Code == parts[0] {
			return l.Code
		}
	}
	script := scriptOf(parts)
	for _, l := range languages {
		lp := strings.Split(l.Code, "-")
		if lp[0] == parts[0] && scriptOf(lp) == script {
			return l.Code
		}
	}
	return ""
}

// FromAcceptLanguage picks the listed language an Accept-Language header asks
// for, most preferred first, or "" when it names none of them.
func FromAcceptLanguage(header string) string {
	type pref struct {
		tag string
		q   float64
	}
	var prefs []pref
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		p := pref{tag: strings.TrimSpace(fields[0]), q: 1}
		for _, f := range fields[1:] {
			if v, ok := strings.CutPrefix(strings.TrimSpace(f), "q="); ok {
				p.q, _ = strconv.ParseFloat(v, 64)
			}
		}
		if p.tag != "" && p.tag != "*" && p.q > 0 {
			prefs = append(prefs, p)
		}
	}
	sort.SliceStable(prefs, func(i, j int) bool { return prefs[i].q > prefs[j].q })
	for _, p := range prefs {
		if code := Match(p.tag); code != "" {
			return code
		}
	}
	return ""
}

// errorMessages are the English error messages, by code.
var errorMessages = func() map[string]string {
	raw, err := files.ReadFile("locales/" + Source + "/errors.json")
	if err != nil {
		panic("i18n: " + err.Error())
	}
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		panic("i18n: errors.json: " + err.Error())
	}
	return out
}()

// HasError reports whether code is in the English error catalog.
func HasError(code string) bool {
	_, ok := errorMessages[code]
	return ok
}

// Message is the English text of an error code, with each {{name}} filled
// in from params. The web UI shows the same code in the reader's language;
// the API sends this text alongside it for everything else. An unknown code
// comes back as itself, which tests catch.
func Message(code string, params map[string]any) string {
	text, ok := errorMessages[code]
	if !ok {
		return code
	}
	return Fill(text, params)
}

// Fill replaces each {{name}} in text with params[name].
func Fill(text string, params map[string]any) string {
	if len(params) == 0 || !strings.Contains(text, "{{") {
		return text
	}
	var b strings.Builder
	for {
		start := strings.Index(text, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], "}}")
		if end < 0 {
			break
		}
		name := strings.TrimSpace(text[start+2 : start+end])
		b.WriteString(text[:start])
		if v, ok := params[name]; ok {
			fmt.Fprint(&b, v)
		} else {
			b.WriteString(text[start : start+end+2])
		}
		text = text[start+end+2:]
	}
	b.WriteString(text)
	return b.String()
}
