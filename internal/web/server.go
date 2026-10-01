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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/export"
	"github.com/vehagn/speaker-promos/internal/lang"
	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/post"
	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/render"
)

//go:embed templates/*.html static/*
var files embed.FS

// Options configures a Server.
type Options struct {
	// Exporter is what the Export buttons write bundles with, and through it
	// the renderer and resolver every page is drawn from — the same ones
	// `promo export` uses, so the preview is what an export would write.
	Exporter *export.Exporter
	// Size is the card size shown by default.
	Size string
	// OutDir is where the Export buttons write, and the Import buttons read.
	OutDir string
}

// Server renders the preview site.
type Server struct {
	opts     Options
	renderer *render.Renderer
	resolver *promo.Resolver
	tmpl     *template.Template

	// mu guards the revision card URLs carry, and pairs it with the manifest
	// state a view was built from. HTMX requests interleave freely — a browser
	// will happily have two edits in flight.
	mu sync.Mutex
	// rev increments on every edit and is embedded in card URLs so the browser
	// refetches exactly the cards that changed.
	rev int64

	// jobs is the running export or import, if any.
	jobs jobs

	// photoMu guards photos. It is separate from mu because filling the cache
	// does network I/O, which must never happen under the page lock.
	photoMu sync.Mutex
	// photos records whether an image could actually be fetched, keyed by the
	// image reference so that changing an override re-probes.
	photos map[string]bool
}

// New builds a Server.
func New(opts Options) (*Server, error) {
	r := opts.Exporter.Renderer
	if opts.Size == "" {
		opts.Size = "portrait"
	}
	if _, err := r.Theme.Size(opts.Size); err != nil {
		return nil, err
	}
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"ago": ago, "day": day,
	}).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	return &Server{
		opts:     opts,
		renderer: r,
		resolver: opts.Exporter.Resolver,
		tmpl:     tmpl,
		rev:      time.Now().Unix(),
		photos:   map[string]bool{},
	}, nil
}

// Handler returns the mux serving the site.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /card/{id}", s.serveCard(false))
	mux.HandleFunc("GET /talk/{id}", s.handleTalkFragment)
	mux.HandleFunc("POST /talk/{id}", s.handleTalkUpdate)
	mux.HandleFunc("POST /speaker/{key}", s.handleSpeakerUpdate)
	mux.HandleFunc("POST /talk/{id}/titlecase", s.handleTalkTitleCase)
	mux.HandleFunc("POST /speaker/{key}/namecase", s.handleSpeakerNameCase)
	mux.HandleFunc("GET /download/{id}", s.serveCard(true))
	mux.HandleFunc("POST /export", s.handleExport)
	mux.HandleFunc("POST /import", s.handleImport)
	mux.HandleFunc("POST /talk/{id}/export", s.handleTalkExport)
	mux.HandleFunc("POST /talk/{id}/import", s.handleTalkImport)
	mux.HandleFunc("GET /jobs/{id}", s.handleJob)
	mux.HandleFunc("POST /settings", s.handleSettings)
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
		// Settings are the global card controls.
		Settings []input
		// FetchedAt is when the program page was read from the website, and
		// ChangedAt when the snapshot last saw it change.
		FetchedAt time.Time
		ChangedAt time.Time
		// OOB is false: the index's rows are the page, not a swap.
		OOB bool
	}{
		Conference: s.resolver.Program.Conference,
		Talks:      views,
		Size:       size,
		Sizes:      s.renderer.Theme.Sizes(),
		Rev:        rev,
		Manifest:   s.resolver.Set.Path(),
		OutDir:     s.opts.OutDir,
		Settings:   controls(s.resolver.Set.Defaults().Over(s.neutral()), "", ""),
		FetchedAt:  s.resolver.Program.FetchedAt,
		ChangedAt:  s.resolver.Source.UpdatedAt(),
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
	out := make([]talkView, 0, len(s.resolver.Program.Talks))
	for _, src := range s.resolver.Program.Talks {
		out = append(out, s.buildViewLocked(s.resolver.Talk(src), size, p))
	}
	return out, s.rev
}

// renderTalk re-renders one talk's row.
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
	src, ok := s.talkParam(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	s.renderTalk(w, r, src, nil)
}

func (s *Server) handleTalkUpdate(w http.ResponseWriter, r *http.Request) {
	src, ok := s.talkParam(w, r, r.PathValue("id"))
	if !ok {
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
	// The sliders are pre-set to what the card uses, so only a value that
	// differs from the global settings is this talk's own.
	spec.Card = readAdjust(r, "card.", s.resolver.Set.Defaults().Over(s.neutral()))
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
		return s.resolver.Set.SetTalk(src.ID, spec)
	})
}

// handleSettings stores the global card controls and redraws every row, since
// every card that does not set its own value follows them.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	a := readAdjust(r, "", s.neutral())
	s.mu.Lock()
	err := s.resolver.Set.SetDefaults(a)
	if err == nil {
		s.rev++
	}
	s.mu.Unlock()
	if err != nil {
		s.renderStatus(w, statusReport{Summary: err.Error(), Error: true})
		return
	}
	var buf strings.Builder
	s.writeRows(&buf, s.sizeParam(r))
	if err := s.tmpl.ExecuteTemplate(&buf, "status.html", statusReport{
		Summary: "card settings saved — every promo without its own follows them", Muted: true,
	}); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(buf.String()))
}

func (s *Server) handleSpeakerUpdate(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	// The row to swap back is passed explicitly: a speaker can appear on
	// several talks, and the one being edited is the one whose form was
	// submitted.
	src, ok := s.talkParam(w, r, r.FormValue("talk"))
	if !ok {
		return
	}
	i := slices.IndexFunc(src.Speakers, func(sp cnd.Speaker) bool { return sp.Key() == key })
	if i < 0 {
		http.NotFound(w, r)
		return
	}
	speaker := src.Speakers[i]

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
	base := promo.Baseline(speaker, s.resolver.Scraped(speaker))
	existing, _ := s.resolver.Set.Speaker(key)

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
	prior := cmp.Or(promo.RoleLine(existing), base.Title)
	fresh := cmp.Or(promo.RoleLine(manifest.SpeakerSpec{
		Employer: cmp.Or(submitted.Employer, base.Employer),
		Job:      cmp.Or(submitted.Job, base.Job),
	}), base.Title)
	if submitted.Title == prior || submitted.Title == fresh {
		submitted.Title = ""
	}
	base.Title = fresh

	spec := submitted.Diff(base)
	// GitHub has no input, so it is carried over rather than diffed — and so
	// is the photo framing when the form had no sliders for it, which it does
	// not while the photo cannot be fetched.
	spec.Links.GitHub = existing.Links.GitHub
	if _, sent := r.Form["photoZoom"]; !sent {
		spec.PhotoX, spec.PhotoY, spec.PhotoZoom = existing.PhotoX, existing.PhotoY, existing.PhotoZoom
	}
	if err := spec.Validate(); err != nil {
		s.fail(w, err)
		return
	}

	s.saveAndRender(w, r, src, func() error {
		return s.resolver.Set.SetSpeaker(key, spec)
	})
}

// serveCard serves one talk's SVG with overrides applied: the same
// self-contained file an export writes, so the preview is the artifact.
//
// A card is addressed by revision, so a given URL's bytes never change and may
// be cached; a download is named the way the CLI names the folder.
func (s *Server) serveCard(download bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, ok := s.resolver.ByID(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		res, err := s.renderer.Card(s.resolver.Program.Conference, t, s.sizeParam(r))
		if err != nil {
			s.fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		if download {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", t.FileStem()+".svg"))
		} else {
			w.Header().Set("Cache-Control", "private, max-age=300")
		}
		w.Write([]byte(res.SVG))
	}
}

// talkParam finds a talk by id, answering 404 when there is none.
func (s *Server) talkParam(w http.ResponseWriter, r *http.Request, id string) (cnd.Talk, bool) {
	t, ok := s.resolver.Program.Talk(id)
	if !ok {
		http.NotFound(w, r)
	}
	return t, ok
}

// sizeParam resolves the requested card size, falling back to the configured
// default rather than erroring on a stale bookmark.
func (s *Server) sizeParam(r *http.Request) string {
	if v := r.FormValue("size"); v != "" {
		if _, err := s.renderer.Theme.Size(v); err == nil {
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
	w.Write([]byte(errorHTML(err)))
}

// errorHTML renders an error for the page, escaped: errors quote file content.
func errorHTML(err error) string {
	return `<p class="error">` + template.HTMLEscapeString(err.Error()) + `</p>`
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
	speakers := s.resolver.Program.Speakers()
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
// They are gathered BEFORE the page lock is taken, because they do network
// I/O: fetching under the lock would let one unresponsive image host block
// every request, not just the row it appears on.
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
	view.Override, _ = s.resolver.Set.Talk(t.ID)
	view.Controls = controls(t.Card.Over(s.neutral()), "card.", view.Anchor())
	for _, sp := range t.Speakers {
		view.Speakers = append(view.Speakers, speakerView{Speaker: sp, HasPhoto: p.photos[sp.Image]})
	}

	in := post.Input{Conference: s.resolver.Program.Conference, Talk: t}
	view.Drafts = []post.Draft{post.LinkedIn(in), post.Bluesky(in)}

	// Inspect, not Card: the warnings come from the text layout, and a full
	// render here would fetch a photo and base64 two fonts for every row on the
	// page — under this lock.
	if res, err := s.renderer.Inspect(s.resolver.Program.Conference, t, size); err == nil {
		view.Warnings = res.Warnings()
	}
	view.Warnings = append(view.Warnings, t.StaleNotes()...)
	return view
}

// exporter is the bundle writer for the given card sizes.
func (s *Server) exporter(sizes []string) *export.Exporter {
	e := *s.opts.Exporter
	e.Sizes = sizes
	return &e
}
