# speaker-promos

Generate speaker promo graphics for [Cloud Native Days Norway](https://cloudnativedays.no)
from the live conference program, plus draft social copy to go with them.

Posting stays manual — this tool removes the copy-paste-retype step, not the judgement.

```sh
go run ./cmd/promo serve                       # preview and edit everything in a browser
go run ./cmd/promo list                        # index the program
go run ./cmd/promo export --all --out out/     # a folder per talk: cards, copy, config
go run ./cmd/promo import out/                 # merge edited promo.yaml files back
go run ./cmd/promo post dario-haaland          # draft LinkedIn / Bluesky copy
```

`serve` is the one to start with: it shows every card next to its draft copy, with the
fields the tool had to guess editable in place.

The 2025 promos were drawn by hand in Inkscape: 23 near-identical 4.7 MB SVGs, each one a
manual copy-paste-retype of a photo, name, employer and talk title. 2026 has 36 talks and
49 speakers.

## Install

```sh
go build -o promo ./cmd/promo
```

Go 1.27 or newer. The only Go dependencies are `golang.org/x/image` (font metrics) and
`gopkg.in/yaml.v3` (themes and manifests). Fonts are vendored under `assets/fonts` (OFL
1.1) and HTMX under `internal/web/static` (0BSD), so the tool needs no network at runtime
beyond the conference website itself.

## Where the data comes from

There is no public API. `cnctl` talks to the website over tRPC, but every schedule and
speaker procedure is an admin procedure, and the Sanity dataset rejects anonymous reads.

What *is* public is the program page itself: `https://2026.cloudnativedays.no/program`
ships the whole schedule in its React Server Components payload, as a series of
`self.__next_f.push([1,"…"])` chunks. Concatenating those gives a flight stream whose rows
hold the conference metadata and a `schedules` array of 2 days × 3 tracks × 72 slots.
`internal/rsc` extracts and parses it, including the `$NN` back-references that long or
deduplicated strings are replaced with — without resolving those, a card would contain a
literal `$55`.

Pages are cached under `~/.cache/cnd-promos` (`--cache-ttl`, `--no-cache`), because the
program page is ~5 MB and each speaker page ~3.4 MB.

### Three kinds of data, kept apart

| | File | Who writes it |
|---|---|---|
| **Source** — what the website said | `source.yaml` (`--source`) | every command, after reading the program |
| **Overrides** — your corrections | `promos.yaml` (`--manifest`) | you, `promo serve` and `promo import` |
| **Output** — what the cards and copy used | each bundle's `promo.yaml`, `Output` | `promo export` |

`source.yaml` is a snapshot of the program and the scraped handles, one `Source` object per
talk. Each talk and speaker has an `updatedAt`: when the tool first saw its current content.
The file is rewritten **only when the website's content changes**, so it is meant to be
committed. `git log -p source.yaml` is then a record of what the CMS changed and when.
The fetch time itself is not stored, because it would change every run; it comes from the
cache, and `promo list` and `promo serve` show both times.

Each override records an `editedAt`, set when the correction actually changes. A correction
made *before* the website last changed that speaker or talk is **stale**. A speaker
retitling themselves after you fixed their role line is the typical case. Stale corrections
are flagged in `promo serve`, in NOTES.txt and in the bundle's `Output.stale`, since the
correction may now be wrong or no longer needed.

`--no-links` no longer means *no handles*: it means *don't fetch*, and uses the handles
last recorded in `source.yaml`.

## Selectors

Every command that takes a talk accepts a `<selector>`, resolved most-specific first:

| Tier | Example |
|---|---|
| Talk id prefix | `09b41694` |
| Speaker slug | `dario-haaland`, `audun-oygard` |
| Talk title substring | `"nok nett"`, `nok-nett` |
| Speaker name substring | `"haaland"` |

The first tier that matches anything wins, so a precise identifier is never ambiguous.
Norwegian letters are folded, so `audun-oygard` finds `audun-øygard`. Use
`--all` to select the whole program.

## `promo list`

```
$ go run ./cmd/promo list
Cloud Native Days Norway 2026 — 26–27 October 2026, Bergen, Norway
36 talks, 49 speakers

DAY  TIME         TRACK               SPEAKERS                      TALK                                          SELECTOR
1    09:00–11:00  Full Day Workshops  Dario Haaland                 Kan 🇳🇴 skyen kjøre på en brødrister?      dario-haaland
1    09:00–11:00  Morning Workshops   Espen Tveitan and Leif Grim…  Scaling Scheduling: the Boring Way — Lesson…  espen-tveitan
…
```

`--day N` and `--speaker <slug|name>` narrow it; `--json` emits the full domain model,
abstracts included.

## `promo export`

```sh
go run ./cmd/promo export dario-haaland                     # one talk's folder
go run ./cmd/promo export --all --size both --out out/      # the whole program
go run ./cmd/promo export --all --formats svg               # skip rasterising
go run ./cmd/promo export --all --width 1000                # smaller PNG/JPEG
```

One folder per talk, named so a directory listing falls into schedule order:

```
out/d1-0900-dario-haaland-kan-skyen-kjore-pa-en-brodrister/
├── portrait.svg      1400×2100, the editable original
├── portrait.png      rasterised at the card's own size
├── portrait.jpg      the same, re-encoded (q88 by default)
├── landscape.svg     1200×630, the site's OG ratio
├── landscape.png
├── landscape.jpg
├── linkedin.txt      the post body, nothing else — paste it verbatim
├── bluesky.txt       likewise, inside the 300-character limit
├── NOTES.txt         what to check first, and the profiles to mention
└── promo.yaml        what the website said, your corrections, and what came out
```

`--formats` picks any of `svg,png,jpg` (all three by default) and `--size` any of
`portrait,landscape,both`. The review notes are a separate file from the copy deliberately:
`linkedin.txt` and `bluesky.txt` hold the post and nothing else, so nothing about guessed
employers can be pasted into a real post by accident.

`promo.yaml` holds the three kinds of data as separate documents:

```yaml
kind: Source            # what the website said: title, abstract, slot, speakers, updatedAt
---
kind: TalkOverride      # editable — the only part an import takes
metadata: {name: <talk id>, editedAt: …, revision: 3f2a9c1e04b7}
spec: {}
---
kind: SpeakerOverride   # one per speaker on the talk, empty until you correct them
metadata: {name: dario-haaland, revision: 44136fa355b3}
spec: {}
---
kind: Output            # what the cards and copy used: role lines, employers and whether
                        # each was guessed, photos found, handles, cards, warnings, stale
```

The overrides carry **your corrections only**. A guessed employer is in `Output`, marked
`employerGuessed: true`, and never in an override, where importing it would confirm it. To
start from a value, copy it from `Output` into the override. The file's header lists every
field.

`Source` and `Output` are ignored when the file is loaded, so a bundle can still be passed
straight back with `--manifest`. There is deliberately no generation timestamp:
`Output.metadata.updatedAt` is the latest of the source and override times, so re-exporting
an unchanged talk produces an identical folder.

`promo export` shows a progress bar on stderr when that is a terminal.

Cards are self-contained: the speaker photo is an embedded JPEG and the typefaces are
base64 `@font-face` rules, so one SVG is the whole artifact. Text is real editable
`<text>`, not outlined paths, so a card can still be adjusted by hand afterwards. A
finished portrait card is ~750 KB against 4.7 MB for the 2025 hand-drawn equivalents.

Only the faces a card actually references get embedded, and photos are requested from the
CDN as JPEG rather than the source PNG (47 KB against 600 KB at 600 px).

The full program at both sizes is 36 folders and 360 files, around 170 MB — nearly all of
it PNG, which is 2.3 MB per portrait card at full size. `--width` trades resolution for
size, and `--formats svg,jpg` skips PNG entirely.

### Fonts

Browsers and [resvg](https://github.com/linebender/resvg) honour the embedded faces.
Inkscape and librsvg ignore base64 `@font-face` and substitute a default, so for the
edit-by-hand and PNG paths:

```sh
go run ./cmd/promo fonts install     # copy the vendored TTFs to ~/Library/Fonts
```

PNG comes from the first of `resvg`, `inkscape`, `rsvg-convert` or `magick` found on PATH.
JPEG is then re-encoded from that PNG with the standard library rather than asked of the
tool, because only one of the four can write JPEG at all — so JPEG is available whenever
PNG is. With no rasteriser installed, the SVGs, copy and manifests are still written and
the run says what to install.

### Emoji

Talk titles contain them — *"Kan 🇳🇴 skyen kjøre på en brødrister?"*. Space
Grotesk has no emoji coverage, so runs whose glyphs are missing from the embedded font are
split into their own `<tspan>` with a system fallback family (`Apple Color Emoji`,
`Noto Color Emoji`) and measured at a 1 em approximation. Their advance is therefore
approximate and an emoji-heavy title can wrap slightly off. `promo export` says which cards
are affected; `--strip-emoji` removes them instead.

### Themes

The built-in theme is the 2026 website brand: the house gradient `#1D4ED8` → `#06B6D4`,
Space Grotesk throughout. Everything — palette, geometry, type scale, autofit ranges — is
data:

```sh
go run ./cmd/promo theme dump > theme.yaml   # start from the built-in theme
go run ./cmd/promo export --all --theme theme.yaml
```

A `--theme` file is merged *over* the default — as YAML, key by key, so it only needs
the keys it changes and can still set one back to `0` or `false`.
Type sizes autofit: each text style has a size range and a maximum line count, and layout
walks the range downward until a real font-metric wrap fits. When nothing fits, the text
is truncated and the card is named in a warning rather than silently clipped.

## `promo post`

```sh
go run ./cmd/promo post dario-haaland
go run ./cmd/promo post --platform bluesky --all
go run ./cmd/promo post --all --language no          # force Norwegian
```

```
── bluesky (259/300 chars) ──

Dario Haaland (Bysten Labs) is running a workshop at Cloud Native Days Norway 2026 🎤

“Kan 🇳🇴 skyen kjøre på en brødrister?”

Plattformer bygges best når teamet forstår hele stacken.

09:00–11:00 · Monday 26 October
https://2026.cloudnativedays.no/program

check before posting:
  - employer for Dario Haaland guessed as "Bysten Labs" from "Bysten Labs" — check it
```

Bluesky copy is assembled to fit 300 characters with the handles and link reserved first,
so shortening never eats the link. LinkedIn copy is longer and lists profile URLs
separately, because LinkedIn only turns a mention into a link when it is picked from its
own autocomplete.

### Norwegian and English

The program is bilingual, and an English sentence introducing a Norwegian abstract reads
badly. The copy is written in the talk's own language:

```
Dario Haaland (Bysten Labs) holder workshop på Cloud Native Days Norway 2026 🎤

«Kan 🇳🇴 skyen kjøre på en brødrister?»

Plattformer bygges best når teamet forstår hele stacken.

09:00–11:00 · mandag 26. oktober
```

Language is detected per talk by counting function words that occur in one language and
not the other — not by looking for æ, ø and å, which are a strong signal when present but
missing from plenty of Norwegian text. The abstract dominates the title by being longer,
which is what you want for a bilingual title like *"Friheten i Koden: Digital Sovereignty
for the Nordic Age"*: the copy follows the language the body is actually written in. Across
the 2026 program this reads 8 talks as Norwegian and 28 as English.

Detection is only a default. `--language en|no` forces one for a run, and `language:` on a
`TalkOverride` forces one for a single talk, which wins over the flag. `promo serve` has a
per-talk selector whose *auto* option names what detection picked, and each draft carries a
badge showing the language it came out in.

What changes in the copy: the verb phrase, the conjunction, the quotation marks (`«»`
rather than `“”`), and the weekday and month names, which are looked up rather than taken
from `time.Format` — that only knows English. The "check before posting" notes stay in
English; they are for you, not for the post.

**The cards follow the talk too, but only where the wording is about the talk.** The
conjunction between speaker names is: a Norwegian card reads *"leffen og Lars"*, and reading
*"and"* there is simply wrong.

The rest of a card's text is left in English on purpose — the date line, the `DAY 1 · …`
slot, the format and level. Those are conference chrome, identical on every card, and the
conference brands itself in English down to the track names ("Full Day Workshops"), so
translating them would make a set of images look inconsistent rather than localised. Say so
if you would rather have the whole card follow the talk.

### The link goes to the program

Posts link to `/program`, not to a speaker profile. Talks have no page of their own: the
site's sitemap carries 49 `/speaker/<slug>` URLs and exactly one `/program`, and the
program page keeps its filters in client state with no URL parameters and no per-talk
anchors — so there is nothing talk-specific to link to, and the program is the closest
thing to "this talk".

Speakers are still surfaced: as `@handle` mentions in the Bluesky body, and as profile URLs
under *profiles to mention* for LinkedIn.

### Employers are guessed

There is no structured employer field. `speaker.title` is free text and inconsistent:
`"Senior Platform Engineer at Vestbit"`, `"Senior Consultant @Nordvik"`,
`"Utvikler hos Bergsdal Consulting"`, `"Bysten Labs"`, `""`. `post` splits on the separators that actually
occur and treats a title with no separator as a bare employer — then **marks every guess
for checking**, as above.

Social handles for @-mentions are scraped from `/speaker/<slug>`, which exposes LinkedIn,
Bluesky, GitHub and X links where a speaker set them. This is the most fragile part of the
tool and is only used for optional mention suggestions; `--no-links` skips it.

Corrections go in a manifest, described below.

## `promo serve`

```sh
go run ./cmd/promo serve                    # http://localhost:8787
go run ./cmd/promo serve --size landscape --addr :9000
```

Every talk as a row: the real card on the left, and on the right its slot, the editable
speaker facts, and both drafts with live character counts. The Bluesky count has a meter
because 300 characters is the binding constraint. Guessed employers are outlined in amber
with the text they were guessed from.

Editing a field saves it to the manifest immediately and re-renders that one card — there
is no save button, and nothing to lose if the browser closes. `git diff promos.yaml` is
the record; `git checkout promos.yaml` is the undo.

- **Name** and **Role line** correct what the CMS has, per speaker.
- The 🪄 beside **Display title** and **Name** fixes capitalisation — see below.
- **Photo** gives a speaker a picture when they have none, or replaces a poor one.
- **Copy language** overrides the detected language; *auto* names what it detected.
- **Display title** shortens a title on the card without touching the program.
- **hidden** excludes a talk from `--all` and from the Export button.
- **Posted** records the date the promo went out. **today** fills it in, and the row gets a
  *posted* badge. It is a manual record: posting stays manual, and the date changes nothing
  else.
- **Export all** writes a bundle per visible talk to `--out`, through the same code as
  `promo export --all`, so the two produce identical folders.
- **Import ← out/** merges the edited `promo.yaml` files back, through the same code as
  `promo import`. It reports every field it changed and every conflict, and re-renders the
  rows. Tick **force** for `--force`.
- Each row has its own **Export** and **Import**, for one talk at a time. Import finds the
  talk's bundle by the id it records, so it still works after a display-title change renamed
  the folder.

Exports and imports run in the background with a progress bar, one at a time; starting a
second one while one runs says so rather than racing it. The header says when the website
data was fetched and last changed, and each row when its website data and its corrections
last changed.

Startup fetches the 49 speaker profile pages once (~3 MB each, then cached on disk), with a
progress bar; after that page loads are instant. `--no-links` skips the fetch and uses the
handles in `source.yaml`. Cards are served as the same
self-contained SVGs you would post, so the preview is the artifact rather than an
approximation — which does mean a fully scrolled page pulls ~24 MB from localhost.

## `promo import`

Merges edited bundle manifests back into the project manifest, closing the loop that
`promo export` opens:

```sh
go run ./cmd/promo import out/                          # every bundle under out/
go run ./cmd/promo import out/d1-0900-dario-haaland*/   # one bundle
go run ./cmd/promo import path/to/promo.yaml            # one file
go run ./cmd/promo import out/ --dry-run                # report, write nothing
```

A directory is searched for `promo.yaml` rather than rejected, since what you have after an
export is `out/` with a folder per talk. `promo serve` has the same thing as an **Import ←
out/** button, sharing this code so the two cannot disagree about what an import covers.

**It is a three-way merge.** Each override in a bundle records the `revision` it had when it
was exported. For every object, the import compares that against the bundle's version and
the project's current one:

| | |
|---|---|
| unchanged in the bundle | skipped. An old bundle never reverts newer work |
| changed only in the bundle | taken **whole**, so a field you deleted is cleared and `hidden: false` unhides |
| changed in both places | a **conflict**: reported, and the project's version kept. `--force` takes the bundle's |

```
$ go run ./cmd/promo import out/
out/d1-0900-dario-haaland-kan-skyen-kjore-pa-en-brodrister/promo.yaml
  SpeakerOverride/dario-haaland employer: "Bysten Labs AS"
  SpeakerOverride/dario-haaland name: "Dario" → (cleared)
  conflict, not applied: SpeakerOverride/dario-haaland image: "a.jpg" → "b.jpg"

2 change(s) from 36 file(s) written to promos.yaml
1 conflict(s) left as the manifest has them: changed in both places since the export.
Re-run with --force to take the bundles' version.
```

Re-importing an unedited bundle reports nothing. An object written by hand, with no
revision, has nothing to merge against, so only the fields it sets are taken.

Bundles from before the three-part layout are refused, with a message to re-export: they
pre-filled every guess, and importing them would confirm every guess at once.

`promo import` no longer needs the program at all, and shows a progress bar on a terminal.

The whole merge is written once, under one lock, so an import either lands completely or
not at all.

## Overrides

`export`, `post` and `serve` all read `promos.yaml` (`--manifest`), a multi-document
Kubernetes-style manifest. It is meant to be committed: it is the record of every
correction made to data the tool guessed.

```yaml
apiVersion: promo.cloudnativedays.no/v1alpha2
kind: SpeakerOverride
metadata:
  name: dario-haaland             # speaker key: the slug, as printed by `promo list`
  editedAt: 2026-10-01T13:54:12Z  # set when the correction changes
spec:
  name: Aurélie Vache             # fixes what the CMS lost; the slug stays put
  employer: Bysten Labs
  job: Infrastructure Engineer
  title: Maintainer, Co-Chair CNCF TAG   # verbatim role line, beats job+employer
  image: photos/dario.jpg         # URL, or a path beside this manifest
  links:
    linkedin: https://www.linkedin.com/in/dario
    bluesky: dario.bsky.social
---
apiVersion: promo.cloudnativedays.no/v1alpha2
kind: TalkOverride
metadata:
  name: 09b41694-27be-495d-abe3-1899bd725ad8   # talk id
spec:
  displayTitle: Kort tittel
  hidden: false
  language: no                                 # en or no; omit to auto-detect
  posted: "2026-10-16"                         # when the promo went out
```

A `v1alpha1` manifest still loads, and is written back as `v1alpha2` on the next save.

An overridden employer stops being reported as a guess, and reaches the card's role line as
well as the copy — the card saying the wrong thing is usually why you are correcting it.

### Names and role lines

`name:` replaces the speaker's name on the card and in the copy. The CMS is where people
typed their own name, so accents go missing — `Aurelie` for `Aurélie` — and there is
nowhere else to fix it. The slug is **not** derived from it: it stays the speaker's
identity, so correcting a name does not rename the export folder or change what
`promo list` tells you to type. A speaker the CMS gave no slug is keyed by their name as
submitted instead (`kyrre-havik`), which is likewise unaffected by correcting it.

`title:` sets the card's role line verbatim instead of composing it from `job` and
`employer`. Real titles often do not fit `<job> at <employer>` — *"Maintainer, Principal
Open source Architect, Co-Chair CNCF TAG Infrastructure"* is one from the 2026 program.
`employer:` still drives what the **post** says, so a `title:` on its own changes the card
and leaves the copy guessing; set both when the guess is also wrong.

### Fixing capitalisation

A 🪄 sits on the **Display title** and **Name** labels. It rewrites that field and saves, so
it behaves exactly like typing the value yourself — including storing nothing when the value
was already right.

Titles follow the talk's language, because the conventions differ:

| | |
|---|---|
| English | Title Case, with minor words lowercase in the middle — *"Shift Left with Reliability Testing"* |
| Norwegian | Sentence case, which is the Norwegian convention — *"Akkurat nok nett: Et datasenter på en laptop"* |

Title-casing a Norwegian title would be an error rather than a correction, and a third of
the program is Norwegian. The language is resolved against the talk's **abstract**, not the
title being fixed: a title is a few words, and *"Praktisk AI-drevet Kubernetes-drift"* reads
as English on its own.

Two rules keep it from making things worse:

- **A word whose spelling someone chose is never touched.** Anything with a capital past its
  first letter (`OpenTelemetry`, `eBPF`, `iOS`) or containing a digit (`k6`, `k8s`, `EC2`).
- **A short list of acronyms is expanded**, including inside hyphenated compounds, so `ai`
  becomes `AI` and `ai-drevet` becomes `AI-drevet` rather than `Ai-drevet`. Without it the
  button would introduce errors of its own.

Names get the first letter of each part capitalised — for the speakers who typed their own
name in lower case — and an ALL-CAPITALS part is title-cased, so *"Abdel SGHIOUAR"* becomes
*"Abdel Sghiouar"*. That is the one place the "spelling someone chose" rule is reversed: in
a name an all-capitals word is someone shouting their surname, whereas in a title it is
probably an acronym (`YAML`, `SBOM`), so titles keep theirs.

`van`, `de`, `von` and friends stay lowercase mid-name even when shouted, single letters are
left alone as initials, and `McDonald`, `O'Brien` and `DuPont` survive untouched.

It is deliberately crude. Both buttons sit next to the field they change, so a wrong result
is visible at once and fixed by typing.

### Photos

Five of the 2026 speakers have no photo and render a monogram of their initials instead,
and a CMS photo is sometimes simply bad. `image:` replaces it with either a URL or a file
on disk; a relative path resolves against the manifest's own directory, so a bundle can
carry its own photo next to the `promo.yaml` that names it.

Transform parameters (crop, size, JPEG re-encode) are only added for the CMS CDN, which is
the only host that understands them — an overridden photo is fetched exactly as given. The
card clips it to the rounded square either way, so an off-square photo is cropped rather
than squashed.

`promo serve` shows a **Photo** field per speaker, outlined in cyan and labelled *none,
showing initials* when the card had to fall back. `hasPhoto` in each bundle's `Output`
says the same thing for a whole export — and it reports whether the photo was actually
*fetched*, so a URL that 404s shows up as `false` rather than looking configured.

`apiVersion`, `kind` and every field name are validated with the line number, so a typo is
an error rather than an override that silently does nothing:

```
promo: promos.yaml: line 6: unknown field "employeer" (known fields: employer, job, links)
```

`promo serve` writes this file sorted by kind then name, so it stays diff-stable, and
removes an object once all of its fields are cleared.

## Layout

```
cmd/promo/          subcommands over stdlib flag
internal/rsc/       flight extraction, row table, $ref resolution
internal/cnd/       the website's model (talk, slot, speaker) and loading it; speaker-page links
internal/cache/     on-disk HTTP cache
internal/theme/     theme structs, YAML loading, embedded default-2026
internal/layout/    font metrics, greedy wrap, size autofit
internal/render/    SVG emitters (portrait, landscape)
internal/lang/      language detection and per-language wording
internal/textcase/  title-case and name capitalisation for the 🪄 buttons
internal/source/    the website snapshot (source.yaml): what changed, and when
internal/promo/     resolves a talk against the manifest, snapshot, handles and language —
                    the one place corrections are applied; everything below reads promo.Talk
internal/post/      LinkedIn / Bluesky copy
internal/raster/    SVG → PNG via an external tool, PNG → JPEG via stdlib
internal/export/    per-talk bundles (Source, overrides, Output), shared by the CLI and the server
internal/manifest/  override manifests: load, validate, save, three-way import
internal/progress/  progress reporting, and the terminal bar
internal/web/       preview server, templates, vendored HTMX
assets/fonts/       vendored OFL fonts + licences
```

## Tests

```sh
go test ./...
```

RSC parsing runs against a committed program-page fixture (including a `$ref` and a
multi-byte length-prefixed row), and the cards against golden SVGs — `go test ./... -update`
rewrites those. Nothing in the test suite touches the network.
