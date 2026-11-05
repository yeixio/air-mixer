# Translations

Air-Mixer's text in every language: the web UI and the messages of the
API's errors. The language list and the way a device's language is matched
are the same as [Matinee's](https://github.com/yeixio/matinee/tree/main/i18n),
so both show the same language on the same device.

```text
i18n/
  languages.json          languages people can pick, with their direction and status
  i18n.go                 the list for the server, how it matches a browser's language,
                          and the English text of each error code
  locales/<language>/     one folder per BCP 47 tag, such as en, pt-BR or zh-Hant
    web.json              the web UI's text
    errors.json           what each error code from the API means
```

English (`en`) is the source. Every other language has the same files and
keys. A key that is missing falls back to English, so a language can ship
before it is complete if `languages.json` marks it `partial`. The server
embeds this folder and serves it at `/i18n/`.

## Languages

English, German (`de`), Spanish (`es`), French (`fr`), Italian (`it`),
Brazilian Portuguese (`pt-BR`), Dutch (`nl`), Polish (`pl`), Swedish
(`sv`), Norwegian Bokmål (`nb`), Danish (`da`), Finnish (`fi`), Czech
(`cs`), Greek (`el`), Hungarian (`hu`), Romanian (`ro`), Turkish (`tr`),
Russian (`ru`), Ukrainian (`uk`), Indonesian (`id`), Malay (`ms`),
Vietnamese (`vi`), Thai (`th`), Hindi (`hi`), Japanese (`ja`), Korean
(`ko`), Simplified Chinese (`zh-Hans`), Traditional Chinese (`zh-Hant`),
Arabic (`ar`), Hebrew (`he`), Persian (`fa`) and Urdu (`ur`). Arabic,
Hebrew, Persian and Urdu are written right to left, and the web UI mirrors
its layout for them.

All but English are machine translated (`status: machine`). The language
picker says so, with a link here, until a person reviews one and its status
becomes `reviewed`.

The web UI starts in the browser's language: the server matches the
`Accept-Language` header exactly (pt-BR), by base language (de-AT → de), by
script (zh-TW → zh-Hant), or by another region (pt-PT → pt-BR), never
Simplified for Traditional Chinese, and falls back to English. The picker in
the header overrides that for one browser; the choice is kept in that
browser's local storage. Numbers and percentages follow the language too.

## Reviewing a translation

Read it in the web UI, fix wording in `locales/<tag>/*.json`, and set the
language's `status` to `reviewed` in `languages.json` in the same pull
request. Keep a term the same everywhere it appears (input server, output
client, route), and keep the tone of the English: short, plain, and talking
to one person.

Run `go test ./i18n ./webui` before you open the pull request.

## Keys

- Keys are stable names, never the English text: `servers.add`, not
  `"Add Server"`. Changing the English wording keeps the key.
- Placeholders use `{{name}}` and must match English in every language. A
  plural form that stands for a single number, such as Arabic's `_two`, may
  spell the number out instead of using `{{count}}`.
- Plurals use i18next suffixes: `key_one` and `key_other` in English, and
  `_zero`, `_two`, `_few` or `_many` where a language needs them (CLDR's
  plural rules, as `Intl.PluralRules` in the browser picks them). Every form
  the language uses must be there; a test checks it.
- Air-Mixer, AirPlay, Apple TV, iPhone, iPad, Mac, ROADMAP.md and API field
  names such as `latency_ms`, `delay_ms`, `source_name`, `mix_mode`,
  `timeout` and `config` stay as they are.
- In the page, an element shows a key with `data-i18n="servers.add"`; its
  placeholder, title or label with `data-i18n-placeholder`,
  `data-i18n-title` or `data-i18n-aria-label`. Script text uses
  `t('servers.added')`. A test checks that every key the page names is in
  English.

## Errors

The API sends each error with a stable `code`, such as
`unsupported_client_protocol`, the English `error` message, and `params`
for any names in it. The web UI shows `errors:<code>` from the catalog,
filled in from `params`, and falls back to `error`. The server words the
English `error` from `locales/en/errors.json` too (`i18n.Message`), so that
file is the one source for the English text.

To add an error, add its code and English text to `locales/en/errors.json`,
return it with `writeCode` in `internal/api` or `invalidCode` in
`internal/service`, and translate the new key in every language.
