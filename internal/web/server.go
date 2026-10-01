// Package web serves a local preview of every promo card beside its draft
// social copy, with the guessed fields editable in place.
//
// It exists because the CLI's review loop is bad: checking 36 cards means
// opening 36 files, the copy is in a terminal next to the card it belongs to
// only by coincidence, and the corrections the tool asks for have to be typed
// into YAML by hand with a re-run to see whether they helped. Here an edit
// re-renders the one card it affects and writes the manifest immediately.
package web

import (
	"cmp"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vehagn/speaker-promos/internal/cache"
	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/export"
	"github.com/vehagn/speaker-promos/internal/lang"
	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/post"
	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/raster"
	"github.com/vehagn/speaker-promos/internal/render"
	"github.com/vehagn/speaker-promos/internal/source"
	"github.com/vehagn/speaker-promos/internal/theme"
)

//go:embed templates/*.html static/*
var files embed.FS

// Options configures a Server.
type Options struct {
	Program *cnd.Program
	Set     *manifest.Set
	// Source is the website snapshot: when things changed, and the handles
	// last scraped. Nil leaves those unknown.
	Source   *source.Snapshot
	Theme    *theme.Theme
	Images   *cache.Cache
	Loader   *cnd.Loader
	Size     string
	OutDir   string
	NoLinks  bool
	NoPhotos bool
	// Formats is what the Export button writes per talk. Empty means all of
	// svg, png and jpg.
	Formats []string
	// RasterWidth and JPEGQuality mirror the export flags; zero means default.
	RasterWidth int
	JPEGQuality int
	// Language is the default copy language; lang.Auto detects it per talk.
	Language lang.Language
}

// Server renders the preview site.
type Server struct {
	opts     Options
	renderer *render.Renderer
	// resolver is the one place a talk meets its corrections, its scraped
	// handles and its language — the same one the CLI uses, so the page shows
	// what an export would write.
	resolver *promo.Resolver
	tmpl     *template.Template

	converter    raster.Converter
	hasConverter bool

	// mu guards the manifest and everything derived from it. HTMX requests
	// interleave freely — a browser will happily have two edits in flight — and
	// every edit both mutates the set and rewrites the file.
	mu sync.Mutex
	// rev increments on every edit and is embedded in card URLs so the browser
	// refetches exactly the cards that changed.
	rev int64
	// linksMu guards links only. It is deliberately separate from mu: filling
	// this cache does network I/O, and doing that under the manifest lock
	// would block every other request for as long as the fetch takes.
	linksMu sync.Mutex
	// links caches scraped social handles for the process lifetime, by speaker
	// key. Each speaker page is ~3 MB; re-fetching on every re-render would
	// make editing unusable even against the on-disk cache.
	links map[string]cnd.Links

	// jobs is the running export or import, if any.
	jobs jobs

	// photoMu guards photos, and is separate from mu for the same reason
	// linksMu is: filling this cache does network I/O.
	photoMu sync.Mutex
	// photos records whether a resolved image could actually be fetched, keyed
	// by the image reference itself so that changing an override re-probes
	// rather than returning the old answer.
	photos map[string]bool
}

// New builds a Server.
func New(opts Options) (*Server, error) {
	if opts.Size == "" {
		opts.Size = "portrait"
	}
	if _, err := opts.Theme.Size(opts.Size); err != nil {
		return nil, err
	}
	renderer, err := render.New(opts.Theme, opts.Images)
	if err != nil {
		return nil, err
	}
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"ago": ago, "day": day,
	}).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	if len(opts.Formats) == 0 {
		opts.Formats = export.AllFormats
	}
	conv, _, hasConv := raster.Find()
	s := &Server{
		opts:         opts,
		renderer:     renderer,
		tmpl:         tmpl,
		converter:    conv,
		hasConverter: hasConv,
		rev:          time.Now().Unix(),
		links:        map[string]cnd.Links{},
		photos:       map[string]bool{},
	}
	fetch := s.fetchLinks
	if opts.NoLinks {
		fetch = nil
	}
	s.resolver = &promo.Resolver{
		Program:  opts.Program,
		Set:      opts.Set,
		Source:   opts.Source,
		Language: opts.Language,
		Links:    opts.Source.LinksFunc(fetch),
	}
	return s, nil
}

// Handler returns the mux serving the site.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /card/{id}", s.handleCard)
	mux.HandleFunc("GET /talk/{id}", s.handleTalkFragment)
	mux.HandleFunc("POST /talk/{id}", s.handleTalkUpdate)
	mux.HandleFunc("POST /speaker/{key}", s.handleSpeakerUpdate)
	mux.HandleFunc("POST /talk/{id}/titlecase", s.handleTalkTitleCase)
	mux.HandleFunc("POST /speaker/{key}/namecase", s.handleSpeakerNameCase)
	mux.HandleFunc("GET /download/{id}", s.handleDownload)
	mux.HandleFunc("POST /export", s.handleExport)
	mux.HandleFunc("POST /import", s.handleImport)
	mux.HandleFunc("POST /talk/{id}/export", s.handleTalkExport)
	mux.HandleFunc("POST /talk/{id}/import", s.handleTalkImport)
	mux.HandleFunc("GET /jobs/{id}", s.handleJob)
	mux.Handle("GET /static/", http.FileServerFS(files))
	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	size := s.sizeParam(r)
	views, rev := s.views(size)

	data := struct {
		Conference cnd.Conference
		Talks      []talkView
		Size       string
		Sizes      []string
		Rev        int64
		Manifest   string
		OutDir     string
		// FetchedAt is when the program page was read from the website, and
		// ChangedAt when the snapshot last saw it change.
		FetchedAt time.Time
		ChangedAt time.Time
		// OOB is false: the index's rows are the page, not a swap.
		OOB bool
	}{
		Conference: s.opts.Program.Conference,
		Talks:      views,
		Size:       size,
		Sizes:      s.opts.Theme.Sizes(),
		Rev:        rev,
		Manifest:   s.opts.Set.Path(),
		OutDir:     s.opts.OutDir,
		FetchedAt:  s.opts.Program.FetchedAt,
		ChangedAt:  s.opts.Source.UpdatedAt(),
	}
	s.renderTemplate(w, "index.html", data)
}

// views builds every talk's view, and returns the manifest revision the cards
// they point at were addressed with. Shared by the index and the import, which
// both replace the whole row list.
func (s *Server) views(size string) ([]talkView, int64) {
	p := s.probe(s.resolver.All())

	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]talkView, 0, len(s.opts.Program.Talks))
	for _, src := range s.opts.Program.Talks {
		out = append(out, s.buildViewLocked(s.resolver.Talk(src), size, p))
	}
	return out, s.rev
}

// renderTalk re-renders one talk's row.
//
// The probes are gathered BEFORE the page lock and the view is built under it.
// That ordering is the point of this helper: probing inside buildViewLocked
// meant one unresponsive image host blocked the lock every render needs, so a
// single dead photo URL wedged the whole server.
//
// err is reported after the view is built rather than instead of it, so a
// failed edit still leaves the row showing what was actually stored.
func (s *Server) renderTalk(w http.ResponseWriter, r *http.Request, src cnd.Talk, err error) {
	view := s.view(src, s.sizeParam(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.renderTemplate(w, "talk.html", view)
}

// view builds one talk's view: probes outside the page lock, the view under it.
func (s *Server) view(src cnd.Talk, size string) talkView {
	p := s.probe([]promo.Talk{s.resolver.Talk(src)})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildViewLocked(s.resolver.Talk(src), size, p)
}

// saveAndRender applies one edit and swaps the row it changed back in.
//
// save runs under s.mu, which guards the revision that card URLs carry;
// bumping it is what makes the browser refetch this card and only this card.
func (s *Server) saveAndRender(w http.ResponseWriter, r *http.Request,
	src cnd.Talk, save func() error) {
	s.mu.Lock()
	err := save()
	if err == nil {
		s.rev++
	}
	s.mu.Unlock()

	s.renderTalk(w, r, src, err)
}

func (s *Server) handleTalkFragment(w http.ResponseWriter, r *http.Request) {
	src, ok := s.opts.Program.Talk(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.renderTalk(w, r, src, nil)
}

func (s *Server) handleTalkUpdate(w http.ResponseWriter, r *http.Request) {
	src, ok := s.opts.Program.Talk(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	spec := manifest.TalkSpec{
		DisplayTitle: strings.TrimSpace(r.FormValue("displayTitle")),
		Hidden:       r.FormValue("hidden") != "",
		Language:     strings.TrimSpace(r.FormValue("language")),
		Posted:       strings.TrimSpace(r.FormValue("posted")),
	}
	// The "today" button beside the date, which is the usual case: you have
	// just posted it.
	if r.FormValue("postedToday") != "" {
		spec.Posted = time.Now().Format(time.DateOnly)
	}
	if _, err := manifest.ParsePosted(spec.Posted); err != nil {
		s.fail(w, err)
		return
	}
	// The title input is pre-filled with the title in use, so submitting the
	// submitted one is not a shortening.
	if spec.DisplayTitle == src.Title {
		spec.DisplayTitle = ""
	}
	if _, err := lang.ParseLanguage(spec.Language); err != nil {
		s.fail(w, err)
		return
	}
	s.saveAndRender(w, r, src, func() error {
		return s.opts.Set.SetTalk(src.ID, spec)
	})
}

func (s *Server) handleSpeakerUpdate(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	// The row to swap back is passed explicitly: a speaker can appear on
	// several talks, and the one being edited is the one whose form was
	// submitted.
	src, ok := s.opts.Program.Talk(r.FormValue("talk"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	var speaker cnd.Speaker
	found := false
	for _, sp := range src.Speakers {
		if sp.Key() == key {
			speaker, found = sp, true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	// The inputs carry the manifest's own field names, so the submission is read
	// through the same table the manifest merges and saves with — adding a field
	// to a speaker needs no change here.
	var submitted manifest.SpeakerSpec
	for _, field := range manifest.SpeakerFields() {
		submitted.SetValue(field, strings.TrimSpace(r.FormValue(field)))
	}
	submitted.Links = submitted.Links.Normalize()

	// The form arrives fully populated, because its inputs are pre-filled with
	// the values in use so they can be edited in place. Only what differs from
	// what was found is a correction; storing the rest would mark every guess
	// as confirmed the first time any field was touched, and put a copy of the
	// whole speaker in the manifest.
	base := promo.Baseline(speaker, s.resolver.Links(speaker))
	existing, _ := s.opts.Set.Speaker(key)

	// The role line is a composed field, which makes it the one field a plain
	// diff cannot judge.
	//
	// It is pre-filled with the line the card draws, composed from employer and
	// job. So when you edit the EMPLOYER, the title input still holds the line
	// composed from the OLD employer — stale, but untouched. Diffing that
	// against the new composition would read it as a deliberate verbatim
	// override and freeze the role line, so employer and job would never drive
	// it again.
	//
	// A submission therefore counts as unedited if it matches either what the
	// field was pre-filled with, or what the other submitted fields now
	// compose to. Typing the old value on purpose is indistinguishable from
	// leaving it, and harmless: the result is the same string.
	prior := promo.RoleLine(existing)
	if prior == "" {
		prior = base.Title
	}
	fresh := promo.RoleLine(manifest.SpeakerSpec{
		Employer: cmp.Or(submitted.Employer, base.Employer),
		Job:      cmp.Or(submitted.Job, base.Job),
	})
	if fresh == "" {
		fresh = base.Title
	}
	if submitted.Title == prior || submitted.Title == fresh {
		submitted.Title = ""
	}
	base.Title = fresh

	spec := submitted.Diff(base)
	// GitHub has no input, so it is carried over rather than diffed.
	spec.Links.GitHub = existing.Links.GitHub

	s.saveAndRender(w, r, src, func() error {
		return s.opts.Set.SetSpeaker(key, spec)
	})
}

func (s *Server) handleCard(w http.ResponseWriter, r *http.Request) {
	svg, _, ok := s.card(r.PathValue("id"), s.sizeParam(r))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	// Cards are addressed with a revision, so a given URL's bytes never change
	// and may be cached hard. An edit bumps the revision and therefore the URL.
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write([]byte(svg))
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	size := s.sizeParam(r)
	svg, t, ok := s.card(id, size)
	if !ok {
		http.NotFound(w, r)
		return
	}
	name := t.FileStem() + ".svg"
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Write([]byte(svg))
}

// card renders one talk's SVG with overrides applied.
func (s *Server) card(id, size string) (string, promo.Talk, bool) {
	t, ok := s.resolver.ByID(id)
	if !ok {
		return "", promo.Talk{}, false
	}
	// Card, not Inspect: this is the image the browser shows and downloads, so
	// it needs the photo and the embedded fonts. Inspect is only for the
	// warnings on the page around it.
	res, err := s.renderer.Card(s.opts.Program.Conference, t, size)
	if err != nil {
		return "", promo.Talk{}, false
	}
	return res.SVG, t, true
}

// sizeParam resolves the requested card size, falling back to the configured
// default rather than erroring on a stale bookmark.
func (s *Server) sizeParam(r *http.Request) string {
	if v := r.FormValue("size"); v != "" {
		if _, err := s.opts.Theme.Size(v); err == nil {
			return v
		}
	}
	return s.opts.Size
}

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data any) {
	var buf strings.Builder
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		// Buffering first means a template failure produces an error page
		// rather than half a page followed by an error.
		s.fail(w, fmt.Errorf("rendering %s: %w", name, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(buf.String()))
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `<p class="error">%s</p>`, template.HTMLEscapeString(err.Error()))
}

// fetchLinks scrapes a speaker's handles, once per process. It is the fetch
// behind the snapshot's links: a result here is recorded in source.yaml, and a
// failure falls back to what the snapshot had.
func (s *Server) fetchLinks(sp cnd.Speaker) (cnd.Links, error) {
	key := sp.Key()
	s.linksMu.Lock()
	l, ok := s.links[key]
	s.linksMu.Unlock()
	if ok {
		return l, nil
	}
	l, err := s.opts.Loader.SpeakerLinks(sp)
	if err != nil {
		return cnd.Links{}, err
	}
	s.linksMu.Lock()
	s.links[key] = l
	s.linksMu.Unlock()
	return l, nil
}

// Warm pre-fetches every speaker's social links and photo.
//
// Without this the first page load fetches 49 speaker pages of ~3 MB each plus
// 49 photos, serially, before rendering anything. Doing it up front makes the
// cost visible and bounded, and the on-disk cache means it only happens once
// per machine. Progress goes to `progress` so the caller can print it.
//
// Photos are warmed even with --no-links, because the page reports which cards
// fell back to a monogram and that answer costs a fetch.
func (s *Server) Warm(parallel int, progress func(done, total int)) {
	speakers := s.opts.Program.Speakers()
	if parallel < 1 {
		parallel = 1
	}

	var wg sync.WaitGroup
	// A small semaphore rather than one goroutine per speaker: these are
	// requests to someone else's website, not a benchmark.
	sem := make(chan struct{}, parallel)
	var mu sync.Mutex
	done := 0

	for _, sp := range speakers {
		wg.Add(1)
		go func(sp cnd.Speaker) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Resolved, since an image override is what decides whether there
			// is a photo at all; resolving also fetches the handles.
			resolved := s.resolver.Talk(cnd.Talk{Speakers: []cnd.Speaker{sp}})
			s.hasPhoto(resolved.Speakers[0].Image)

			mu.Lock()
			done++
			n := done
			mu.Unlock()
			if progress != nil {
				progress(n, len(speakers))
			}
		}(sp)
	}
	wg.Wait()
}

// probes are the network-dependent answers a view needs that resolving does not
// already cache: whether each photo can actually be fetched, by image.
//
// They are gathered BEFORE the page lock is taken. Doing the fetching inside
// buildViewLocked meant one unresponsive image host blocked the lock every
// render needs, so a single dead photo URL wedged the whole server — not just
// the page it appeared on. Reproduced against a host that accepts the
// connection and never answers, then fixed here and bounded by a timeout in
// internal/cache.
type probes struct {
	photos map[string]bool
}

// probe gathers the answers for the given talks. It must NOT be called with
// s.mu held: it does network I/O. Resolving the talks first is what fetched
// their speakers' handles, so those are cached by the time a view is built.
func (s *Server) probe(talks []promo.Talk) probes {
	out := probes{photos: map[string]bool{}}
	for _, t := range talks {
		for _, sp := range t.Speakers {
			out.photos[sp.Image] = s.hasPhoto(sp.Image)
		}
	}
	return out
}

// hasPhoto reports whether a photo can be fetched, once per image.
func (s *Server) hasPhoto(image string) bool {
	if image == "" {
		return false
	}
	s.photoMu.Lock()
	ok, seen := s.photos[image]
	s.photoMu.Unlock()
	if seen {
		return ok
	}

	ok = s.renderer.HasPhoto(image)

	s.photoMu.Lock()
	s.photos[image] = ok
	s.photoMu.Unlock()
	return ok
}

// buildViewLocked assembles a talk's view. Callers must hold s.mu, and must
// have gathered p outside it.
func (s *Server) buildViewLocked(t promo.Talk, size string, p probes) talkView {
	view := talkView{
		Talk:    t,
		Size:    size,
		CardURL: fmt.Sprintf("/card/%s?size=%s&rev=%d", t.ID, size, s.rev),
	}
	view.WebsiteAt, view.EditedAt = t.UpdatedAt, t.EditedAt
	for _, sp := range t.Speakers {
		view.WebsiteAt = latest(view.WebsiteAt, sp.UpdatedAt)
		view.EditedAt = latest(view.EditedAt, sp.EditedAt)
		if sp.Stale {
			view.Warnings = append(view.Warnings, fmt.Sprintf(
				"the website changed %s's details after they were corrected — check the correction still holds", sp.Name))
		}
	}
	if t.Stale {
		view.Warnings = append(view.Warnings,
			"the website changed this talk after its display title or language was set — check it still holds")
	}
	view.Override, _ = s.opts.Set.Talk(t.ID)
	for _, sp := range t.Speakers {
		view.Speakers = append(view.Speakers, speakerView{Speaker: sp, HasPhoto: p.photos[sp.Image]})
	}

	in := post.Input{Conference: s.opts.Program.Conference, Talk: t}
	view.Drafts = []draftView{
		{Draft: post.LinkedIn(in)},
		{Draft: post.Bluesky(in), Limit: post.BlueskyLimit},
	}
	for i := range view.Drafts {
		d := &view.Drafts[i]
		d.Over = d.Limit > 0 && d.Draft.Runes() > d.Limit
	}

	// Inspect, not Card: the warnings come from the text layout, and a full
	// render here would fetch a photo and base64 two fonts for every row on the
	// page — under this lock.
	if res, err := s.renderer.Inspect(s.opts.Program.Conference, t, size); err == nil {
		if res.EmojiFallback {
			view.Warnings = append(view.Warnings,
				"contains emoji: renders in browsers, but Inkscape and librsvg leave a gap")
		}
		for _, el := range res.Overflow {
			view.Warnings = append(view.Warnings, "text truncated to fit: "+el)
		}
	}
	return view
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// exporter builds the bundle writer for the given card sizes.
//
// It is constructed per request rather than held on the Server because the
// requested size comes from the query string, and because the manifest it reads
// changes with every edit. Callers must hold s.mu.
func (s *Server) exporter(sizes []string) *export.Exporter {
	return &export.Exporter{
		Renderer:     s.renderer,
		Resolver:     s.resolver,
		Formats:      s.opts.Formats,
		Sizes:        sizes,
		RasterWidth:  s.opts.RasterWidth,
		JPEGQuality:  s.opts.JPEGQuality,
		Converter:    s.converter,
		HasConverter: s.hasConverter,
	}
}
