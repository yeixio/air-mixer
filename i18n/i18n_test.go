package i18n

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	for tag, want := range map[string]string{
		"de": "de", "de-AT": "de", "pt-BR": "pt-BR", "pt-PT": "pt-BR", "pt": "pt-BR",
		"zh-TW": "zh-Hant", "zh-HK": "zh-Hant", "zh-Hant-TW": "zh-Hant", "zh-CN": "zh-Hans", "zh": "zh-Hans",
		"no": "nb", "nb-NO": "nb", "iw": "he", "en_GB": "en", "xx": "", "": "",
	} {
		if got := Match(tag); got != want {
			t.Errorf("Match(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestFromAcceptLanguage(t *testing.T) {
	for header, want := range map[string]string{
		"de-AT,de;q=0.9,en;q=0.8": "de",
		"xx,fr;q=0.5":             "fr",
		"en;q=0.2,ja":             "ja",
		"*":                       "",
		"":                        "",
	} {
		if got := FromAcceptLanguage(header); got != want {
			t.Errorf("FromAcceptLanguage(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestLanguagesListed(t *testing.T) {
	seen := map[string]bool{}
	for i, l := range Languages() {
		if i == 0 && l.Code != Source {
			t.Errorf("first language %q, want %q", l.Code, Source)
		}
		if seen[l.Code] || l.Name == "" || (l.Dir != "ltr" && l.Dir != "rtl") || Match(l.Code) != l.Code {
			t.Errorf("bad language %+v", l)
		}
		if !slices.Contains([]string{"source", "machine", "partial", "reviewed", "community"}, l.Status) {
			t.Errorf("%s: unknown status %q", l.Code, l.Status)
		}
		seen[l.Code] = true
	}
	if Dir("ar") != "rtl" || Dir("de") != "ltr" || Dir("xx") != "ltr" {
		t.Error("Dir is wrong")
	}
}

func TestMessage(t *testing.T) {
	if got := Message("unsupported_client_protocol", map[string]any{"protocol": "dlna"}); got != `unsupported client protocol "dlna"` {
		t.Errorf("Message = %q", got)
	}
	if got := Message("delay_range", map[string]any{"max": 5000}); got != "delay_ms must be between 0 and 5000" {
		t.Errorf("Message = %q", got)
	}
	if got := Message("no_such_code", nil); got != "no_such_code" {
		t.Errorf("Message of an unknown code = %q", got)
	}
	if got := Fill("{{a}} and {{ b }} but not {{c}}", map[string]any{"a": 1, "b": "two"}); got != "1 and two but not {{c}}" {
		t.Errorf("Fill = %q", got)
	}
}

// pluralCategories are the CLDR cardinal plural categories of each listed
// language, which i18next picks a key's suffix from (Intl.PluralRules in
// the browser): every one a language uses must be in its catalog, or that
// number shows in English.
var pluralCategories = map[string][]string{
	"en": {"one", "other"}, "de": {"one", "other"}, "es": {"one", "many", "other"}, "fr": {"one", "many", "other"},
	"it": {"one", "many", "other"}, "pt-BR": {"one", "many", "other"}, "nl": {"one", "other"},
	"pl": {"one", "few", "many", "other"}, "sv": {"one", "other"}, "nb": {"one", "other"}, "da": {"one", "other"},
	"fi": {"one", "other"}, "cs": {"one", "few", "many", "other"}, "el": {"one", "other"}, "hu": {"one", "other"},
	"ro": {"one", "few", "other"}, "tr": {"one", "other"}, "uk": {"one", "few", "many", "other"},
	"ru": {"one", "few", "many", "other"}, "id": {"other"}, "ms": {"other"}, "vi": {"other"}, "th": {"other"},
	"hi": {"one", "other"}, "ja": {"other"}, "ko": {"other"}, "zh-Hans": {"other"}, "zh-Hant": {"other"},
	"ar": {"zero", "one", "two", "few", "many", "other"}, "he": {"one", "two", "other"}, "fa": {"one", "other"},
	"ur": {"one", "other"},
}

var (
	pluralSuffix = regexp.MustCompile(`_(zero|one|two|few|many|other)$`)
	placeholder  = regexp.MustCompile(`\{\{\s*([^},\s]+)[^}]*\}\}`)
	markup       = regexp.MustCompile(`</?[a-z]+>`)
)

func baseKey(key string) string { return pluralSuffix.ReplaceAllString(key, "") }

func placeholders(text string) []string {
	var out []string
	for _, m := range placeholder.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

func tags(text string) []string {
	out := markup.FindAllString(text, -1)
	sort.Strings(out)
	return out
}

// flatten reads a catalog file into dotted keys, failing on anything but
// text and objects of keys, and on a key repeated in one object, which
// encoding/json would silently drop.
func flatten(t *testing.T, name string, raw []byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	var walk func(prefix string) bool
	walk = func(prefix string) bool {
		tok, err := dec.Token()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			return false
		}
		switch v := tok.(type) {
		case string:
			out[prefix] = v
			return true
		case json.Delim:
			if v != '{' {
				t.Errorf("%s: %s must be text or an object of keys", name, prefix)
				return false
			}
			seen := map[string]bool{}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					t.Errorf("%s: %v", name, err)
					return false
				}
				key := k.(string)
				if seen[key] {
					t.Errorf("%s: duplicate key %q in %q", name, key, prefix)
				}
				seen[key] = true
				full := key
				if prefix != "" {
					full = prefix + "." + key
				}
				if !walk(full) {
					return false
				}
			}
			_, err := dec.Token()
			return err == nil
		default:
			t.Errorf("%s: %s must be text or an object of keys", name, prefix)
			return false
		}
	}
	walk("")
	return out
}

// readLocale reads every namespace of a language.
func readLocale(t *testing.T, code string) map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]string{}
	entries, err := fs.ReadDir(Files(), "locales/"+code)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := path.Join("locales", code, e.Name())
		if !strings.HasSuffix(name, ".json") {
			t.Errorf("%s: only .json files belong in a locale", name)
			continue
		}
		raw, err := fs.ReadFile(Files(), name)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(e.Name(), ".json")] = flatten(t, name, raw)
	}
	return out
}

// singleNumbers are the plural categories that stand for one number only
// in a language, such as Arabic's _two, so their text may spell the number
// out instead of using {{count}}. Russian's _one also covers 21, 31 and so
// on, and French's covers 0 and 1, so theirs may not.
var singleNumbers = map[string][]string{
	"en": {"one"}, "de": {"one"}, "es": {"one"}, "it": {"one"}, "nl": {"one"}, "pl": {"one"}, "sv": {"one"},
	"nb": {"one"}, "da": {"one"}, "fi": {"one"}, "cs": {"one"}, "el": {"one"}, "hu": {"one"}, "ro": {"one"},
	"tr": {"one"}, "ar": {"zero", "one", "two"}, "he": {"one", "two"}, "ur": {"one"},
}

func singleNumber(code, category string) bool { return slices.Contains(singleNumbers[code], category) }

// compare lists every way a language's catalog differs from English that
// would show wrong text.
func compare(code string, en, tr map[string]map[string]string, partial bool) []string {
	var problems []string
	categories := pluralCategories[code]
	for ns, keys := range tr {
		enKeys, ok := en[ns]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s/%s.json has no English file", code, ns))
			continue
		}
		bases := map[string]bool{}
		for k := range enKeys {
			bases[baseKey(k)] = true
		}
		for key, text := range keys {
			base := baseKey(key)
			if !bases[base] {
				problems = append(problems, fmt.Sprintf("%s %s:%s is not in English", code, ns, key))
				continue
			}
			enText, ok := enKeys[key]
			if !ok {
				enText = enKeys[base+"_other"]
			}
			own, want := placeholders(text), placeholders(enText)
			category := ""
			if m := pluralSuffix.FindStringSubmatch(key); m != nil {
				category = m[1]
				if !slices.Contains(categories, category) {
					problems = append(problems, fmt.Sprintf("%s %s:%s is a plural form %s doesn't use", code, ns, key, code))
				}
				for _, c := range categories {
					if _, ok := keys[base+"_"+c]; !ok {
						problems = append(problems, fmt.Sprintf("%s %s:%s has no _%s form", code, ns, base, c))
					}
				}
			}
			if category != "" && singleNumber(code, category) && !slices.Contains(own, "count") {
				want = slices.DeleteFunc(slices.Clone(want), func(s string) bool { return s == "count" })
			}
			if !slices.Equal(own, want) {
				problems = append(problems, fmt.Sprintf("%s %s:%s has placeholders %v, English has %v", code, ns, key, own, placeholders(enText)))
			}
			if !slices.Equal(tags(text), tags(enText)) {
				problems = append(problems, fmt.Sprintf("%s %s:%s has markup %v, English has %v", code, ns, key, tags(text), tags(enText)))
			}
			if strings.TrimSpace(text) == "" {
				problems = append(problems, fmt.Sprintf("%s %s:%s is empty", code, ns, key))
			}
		}
	}
	if !partial {
		for ns, enKeys := range en {
			have := map[string]bool{}
			for k := range tr[ns] {
				have[baseKey(k)] = true
			}
			for k := range enKeys {
				if !have[baseKey(k)] {
					problems = append(problems, fmt.Sprintf("%s %s:%s is missing, so it shows in English", code, ns, baseKey(k)))
				}
			}
		}
	}
	sort.Strings(problems)
	return slices.Compact(problems)
}

func TestCatalog(t *testing.T) {
	dirs, err := fs.ReadDir(Files(), "locales")
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []string
	for _, d := range dirs {
		onDisk = append(onDisk, d.Name())
	}
	var listed []string
	for _, l := range Languages() {
		listed = append(listed, l.Code)
		if _, ok := pluralCategories[l.Code]; !ok {
			t.Errorf("%s: add its plural categories to pluralCategories", l.Code)
		}
	}
	sort.Strings(onDisk)
	sort.Strings(listed)
	if !slices.Equal(onDisk, listed) {
		t.Errorf("locales/ has %v, languages.json lists %v", onDisk, listed)
	}

	en := readLocale(t, Source)
	if len(en["web"]) == 0 || len(en["errors"]) == 0 {
		t.Fatal("English needs web.json and errors.json")
	}
	for ns, keys := range en {
		for key := range keys {
			if m := pluralSuffix.FindStringSubmatch(key); m != nil {
				for _, c := range pluralCategories[Source] {
					if _, ok := keys[baseKey(key)+"_"+c]; !ok {
						t.Errorf("en %s:%s has no _%s form", ns, baseKey(key), c)
					}
				}
			}
		}
	}
	for _, l := range Languages() {
		if l.Code == Source {
			continue
		}
		t.Run(l.Code, func(t *testing.T) {
			for _, p := range compare(l.Code, en, readLocale(t, l.Code), l.Status == "partial") {
				t.Error(p)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	en := map[string]map[string]string{"web": {"title": "Speakers", "count_one": "{{count}} speaker", "count_other": "{{count}} speakers", "hello": "Hi {{name}}"}}
	de := map[string]map[string]string{
		"web":   {"title": "Lautsprecher", "extra": "x", "count_one": "{{anzahl}} Lautsprecher", "hello": "Hallo {{name}}"},
		"other": {"a": "b"},
	}
	want := []string{
		"de web:count has no _other form",
		"de web:count_one has placeholders [anzahl], English has [count]",
		"de web:extra is not in English",
		"de/other.json has no English file",
	}
	if got := compare("de", en, de, true); !slices.Equal(got, want) {
		t.Errorf("compare(de) =\n%v\nwant\n%v", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	es := map[string]map[string]string{"web": {"title": "Altavoces", "count_one": "{{count}} altavoz", "count_other": "{{count}} altavoces", "hello": "Hola {{name}}"}}
	if got := compare("es", en, es, false); !slices.Equal(got, []string{"es web:count has no _many form"}) {
		t.Errorf("compare(es) = %v", got)
	}
	ja := map[string]map[string]string{"web": {"title": "スピーカー", "count_other": "{{count}} 台", "hello": "こんにちは {{name}}"}}
	if got := compare("ja", en, ja, false); len(got) != 0 {
		t.Errorf("compare(ja) = %v", got)
	}
	ar := map[string]map[string]string{"web": {"title": "مكبرات", "hello": "مرحبا {{name}}",
		"count_zero": "لا مكبرات", "count_one": "مكبر واحد", "count_two": "مكبران", "count_few": "{{count}} مكبرات",
		"count_many": "{{count}} مكبرًا", "count_other": "{{count}} مكبر"}}
	if got := compare("ar", en, ar, false); len(got) != 0 {
		t.Errorf("compare(ar) = %v", got)
	}
	if got := compare("de", en, map[string]map[string]string{"web": {"title": "x"}}, false); !slices.Contains(got, "de web:hello is missing, so it shows in English") {
		t.Errorf("compare(de) without hello = %v", got)
	}
}
