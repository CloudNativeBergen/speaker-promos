package render

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/vehagn/speaker-promos/internal/cache"
	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/layout"
	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/theme"
)

// Renderer draws promo cards for one conference with one theme.
type Renderer struct {
	Theme *theme.Theme
	Fonts *layout.FontSet
	// Images fetches speaker photos. Nil disables photos, which is what keeps
	// the golden tests offline.
	Images *cache.Cache
	// StripEmoji drops emoji from text instead of routing them to a fallback
	// family. Useful when a card is destined for a renderer with no colour
	// emoji font, where they would otherwise come out as tofu.
	StripEmoji bool
}

// New builds a renderer for a theme.
func New(t *theme.Theme, images *cache.Cache) (*Renderer, error) {
	fonts, err := layout.NewFontSet(t)
	if err != nil {
		return nil, err
	}
	return &Renderer{Theme: t, Fonts: fonts, Images: images}, nil
}

// Result is a rendered card plus anything about it the caller should know.
type Result struct {
	SVG string
	// Width and Height are the card's pixel dimensions, so a caller
	// rasterising it does not have to re-read the theme to learn them.
	Width  int
	Height int
	// EmojiFallback is true when some text needed the system emoji family.
	// Browsers and resvg honour it; Inkscape and librsvg do not substitute a
	// colour emoji font and leave a gap instead, so this is worth telling the
	// user about rather than letting them discover it in an export.
	EmojiFallback bool
	// Overflow names the elements whose text had to be truncated to fit, so a
	// theme with too tight a type scale is visible rather than silent.
	Overflow []string
}

// Warnings are what a user should hear about the card: emoji some renderers
// drop, and text that had to be cut to fit.
func (r Result) Warnings() []string {
	var out []string
	if r.EmojiFallback {
		out = append(out, "contains emoji, which Inkscape and librsvg drop")
	}
	for _, el := range r.Overflow {
		out = append(out, "text truncated to fit ("+el+")")
	}
	return out
}

// Card renders one talk at one card size.
//
// The talk's language selects the wording — currently just the conjunction
// between speaker names, which must be "og" on a Norwegian talk.
func (r *Renderer) Card(conf cnd.Conference, t promo.Talk, size string) (Result, error) {
	return r.render(conf, t, size, false)
}

// Inspect reports what rendering a card would warn about — truncated text, or
// emoji a renderer will drop — without producing a usable card.
//
// The preview server needs those warnings for every row on the page. They come
// from the text layout alone, so this skips fetching photos and embedding
// fonts — the expensive part of a render, and the only network I/O.
//
// The returned SVG is not a card and must not be served.
func (r *Renderer) Inspect(conf cnd.Conference, t promo.Talk, size string) (Result, error) {
	return r.render(conf, t, size, true)
}

func (r *Renderer) render(conf cnd.Conference, t promo.Talk, size string, inspect bool) (Result, error) {
	g, err := r.Theme.Size(size)
	if err != nil {
		return Result{}, err
	}
	p := &pass{
		faces:   map[string]bool{},
		inspect: inspect,
	}

	var svg string
	switch size {
	case "landscape":
		svg, err = r.landscape(conf, t, g, p)
	default:
		svg, err = r.portrait(conf, t, g, p)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{
		SVG:           svg,
		Width:         g.Width,
		Height:        g.Height,
		EmojiFallback: p.emoji,
		Overflow:      p.overflow,
	}, nil
}

// pass is the state gathered while drawing one card.
//
// faces records which theme faces were actually referenced, so only those get
// embedded — carrying all three Space Grotesk weights when a card uses two adds
// ~150 KB per file for nothing.
type pass struct {
	faces    map[string]bool
	emoji    bool
	overflow []string
	// inspect asks for the warnings only. It skips fetching photos and
	// base64-encoding fonts, which is everything expensive about a render and
	// nothing the warnings depend on.
	inspect bool
}

func (p *pass) face(key string) {
	if key == "" {
		key = "body"
	}
	p.faces[key] = true
}

// prelude builds everything that must precede a card's content: the root
// element, the embedded @font-face rules, the brand gradient and the backdrop.
//
// It is composed AFTER the body has been drawn, because which faces to embed is
// only known once the content has asked for them — the `used` set is filled in
// by r.text as it lays each element out. Writing the <style> block up front
// embedded an empty set and produced cards that silently fell back to whatever
// the renderer had installed.
func (r *Renderer) prelude(g theme.Geometry, p *pass) string {
	c := &canvas{}
	c.writef(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="%d" height="%d" viewBox="0 0 %d %d">`+"\n",
		g.Width, g.Height, g.Width, g.Height)

	var embed []embeddedFace
	for key := range p.faces {
		face, ok := r.Theme.Fonts[key]
		if !ok {
			continue
		}
		data, ok := r.Fonts.Raw(key)
		if !ok {
			continue
		}
		embed = append(embed, embeddedFace{Family: face.Family, Weight: face.Weight, Style: face.Style, Data: data})
	}
	// Deterministic order keeps output byte-stable across runs, which is what
	// makes the golden tests meaningful and diffs reviewable.
	sortFaces(embed)
	if !p.inspect {
		c.write(fontFaceCSS(embed))
	}

	pal := r.Theme.Palette
	c.writef(`  <defs>
    <linearGradient id="brand" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color=%q/>
      <stop offset="1" stop-color=%q/>
    </linearGradient>
  </defs>
`, pal.GradientFrom, pal.GradientTo)
	c.writef("  <rect width=\"%d\" height=\"%d\" fill=\"url(#brand)\"/>\n", g.Width, g.Height)
	r.backdrop(c, g)
	return c.String()
}

// backdrop paints the soft translucent circles the website's own share cards
// use, so a promo reads as part of the same family.
func (r *Renderer) backdrop(c *canvas, g theme.Geometry) {
	w, h := float64(g.Width), float64(g.Height)
	circles := []struct{ cx, cy, rad, op float64 }{
		{w * 0.88, h * 0.06, w * 0.30, 0.07},
		{w * 0.05, h * 0.42, w * 0.26, 0.06},
		{w * 0.95, h * 0.78, w * 0.34, 0.05},
	}
	c.write("  <g fill=\"#FFFFFF\">\n")
	for _, ci := range circles {
		c.writef("    <circle cx=\"%s\" cy=\"%s\" r=\"%s\" opacity=\"%s\"/>\n",
			num(ci.cx), num(ci.cy), num(ci.rad), num(ci.op))
	}
	c.write("  </g>\n")
}

// photo draws a speaker's photo as a rounded square, or a monogram placeholder
// when there is no image.
//
// Photos are embedded as data URIs so a card is a single self-contained file —
// the 2025 promos were hand-built the same way. A remote <image href> would
// leave a card that breaks when the CDN URL rotates.
func (r *Renderer) photo(c *canvas, p *pass, sp promo.Speaker, x, y, size, radius float64) {
	clip := fmt.Sprintf("photo-%s", strings.ReplaceAll(sp.Source.Slug+sp.Source.ID, " ", ""))
	if clip == "photo-" {
		clip = "photo-anon"
	}

	c.writef("  <defs><clipPath id=%q><rect x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\" rx=\"%s\"/></clipPath></defs>\n",
		clip, num(x), num(y), num(size), num(size), num(radius))

	if data, mime, ok := r.photoData(p, sp.Image, int(size)); ok {
		c.writef("  <image x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\" clip-path=\"url(#%s)\" preserveAspectRatio=\"xMidYMid slice\" xlink:href=\"data:%s;base64,%s\"/>\n",
			num(x), num(y), num(size), num(size), clip, mime, base64.StdEncoding.EncodeToString(data))
	} else {
		c.writef("  <rect x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\" rx=\"%s\" %s/>\n",
			num(x), num(y), num(size), num(size), num(radius),
			parsePaint(r.Theme.Palette.CardFill).fillAttrs(1))
		r.monogram(c, sp.Name, x+size/2, y+size/2, size)
	}

	c.writef("  <rect x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\" rx=\"%s\" fill=\"none\" %s stroke-width=\"%s\"/>\n",
		num(x), num(y), num(size), num(size), num(radius),
		parsePaint(r.Theme.Palette.PhotoStroke).strokeAttrs(1), num(size/160+2))
}

// drawPhotos paints a row of speaker photos at the left edges photoRow
// computed. Speakers past the last position are the ones the row caps at.
func (r *Renderer) drawPhotos(c *canvas, p *pass, speakers []promo.Speaker,
	lefts []float64, y, size, radius float64) {
	for i, sp := range speakers {
		if i >= len(lefts) {
			break
		}
		r.photo(c, p, sp, lefts[i], y, size, radius)
	}
}

// monogram draws a speaker's initials, used when no photo exists.
func (r *Renderer) monogram(c *canvas, name string, cx, cy, box float64) {
	letters := initials(name)
	if letters == "" {
		return
	}
	f, err := r.Fonts.Face("heading")
	if err != nil {
		return
	}
	size := box * 0.34
	// Centre the glyphs vertically by nudging the baseline down by roughly a
	// third of the cap height.
	c.textLine(letters, f, r.Theme.EmojiFallback, cx, cy+size*0.34, size, 0.02, "middle",
		parsePaint(r.Theme.Palette.Text), 0.85)
}

// initials returns up to two initials for a name.
func initials(name string) string {
	parts := strings.Fields(name)
	var out []rune
	for _, p := range parts {
		for _, r := range p {
			out = append(out, []rune(strings.ToUpper(string(r)))[0])
			break
		}
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

// HasPhoto reports whether a speaker's photo can actually be fetched.
//
// This is not the same as "image is set": an URL that 404s, or a local path
// that does not exist, also lands on a monogram, and callers reporting what a
// card shows need the fetched answer rather than the configured one.
//
// The check asks for the SMALLEST rendition the size clamp allows. Whether a
// photo exists does not depend on the size requested, and a card's own request
// size varies with its geometry and speaker count, so there is no single size
// that would always reuse the card's cache entry — a small one at least keeps
// the miss cheap.
func (r *Renderer) HasPhoto(image string) bool {
	_, _, ok := r.fetchPhoto(image, 0)
	return ok
}

// photoData is fetchPhoto unless this is an inspection pass, which must not
// touch the network.
func (r *Renderer) photoData(p *pass, image string, size int) ([]byte, string, bool) {
	if p != nil && p.inspect {
		return nil, "", false
	}
	return r.fetchPhoto(image, size)
}

// fetchPhoto downloads a speaker photo, returning its bytes and MIME type.
func (r *Renderer) fetchPhoto(image string, size int) ([]byte, string, bool) {
	src := cnd.ImageSourceOf(image, photoRequestSize(size))
	if src.Empty() {
		return nil, "", false
	}

	var data []byte
	switch {
	case src.Path != "":
		// A photo supplied by an override, sitting next to the manifest. Read
		// regardless of r.Images: that switch is about not hitting the network,
		// and a local file is not the network.
		b, err := os.ReadFile(src.Path)
		if err != nil {
			return nil, "", false
		}
		data = b
	case r.Images == nil:
		return nil, "", false
	default:
		b, err := r.Images.Get(src.URL)
		if err != nil {
			return nil, "", false
		}
		data = b
	}

	if len(data) == 0 {
		return nil, "", false
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return nil, "", false
	}
	return data, mime, true
}

// photoRequestSize asks the CDN for a rendition at twice the drawn size,
// capped, so a card exported to PNG at 2x still has real detail.
func photoRequestSize(drawn int) int {
	want := drawn * 2
	switch {
	case want < 200:
		return 200
	case want > 1200:
		return 1200
	default:
		return want
	}
}

// text draws a theme text element with its TOP edge at y, and returns the
// height it occupied.
//
// Taking the top edge rather than a baseline is what keeps the stack honest.
// SVG positions text by baseline, so a baseline-taking API forces every caller
// to add an ascent — and the only ascent a caller can know up front is the
// style's maximum size, which is wrong whenever autofit scaled the text down.
// That mismatch put the first baseline too low while the returned height was
// computed from the smaller fitted size, so the next block overlapped this one.
// Here the ascent comes from the block that was actually laid out.
func (r *Renderer) text(c *canvas, p *pass, g theme.Geometry, element, content string, x, y, maxWidth float64, anchor string) (float64, error) {
	st, ok := g.Text[element]
	if !ok {
		return 0, fmt.Errorf("theme %q has no text style %q", r.Theme.Name, element)
	}
	if content == "" {
		return 0, nil
	}
	if st.Uppercase {
		content = strings.ToUpper(content)
	}
	if r.StripEmoji {
		content = r.stripEmoji(st.Face, content)
		if strings.TrimSpace(content) == "" {
			return 0, nil
		}
	}

	block, f, err := r.Fonts.FitStyle(st, content, maxWidth)
	if err != nil {
		return 0, err
	}
	p.face(st.Face)
	if block.Overflow {
		p.overflow = append(p.overflow, element)
	}
	for _, line := range block.Lines {
		if layout.HasFallback(layout.SplitRuns(f, line)) {
			p.emoji = true
			break
		}
	}
	opacity := st.Opacity
	if opacity == 0 {
		opacity = 1
	}
	c.textBlock(block, f, r.Theme.EmojiFallback, x, y+ascent(block.Size), anchor,
		parsePaint(r.Theme.Paint(st.Color)), opacity)
	return block.Height(), nil
}

// stripEmoji removes runes the face cannot render, collapsing the whitespace
// they leave behind.
func (r *Renderer) stripEmoji(faceKey, s string) string {
	f, err := r.Fonts.Face(faceKey)
	if err != nil {
		return s
	}
	var b strings.Builder
	for _, run := range layout.SplitRuns(f, s) {
		if !run.Fallback {
			b.WriteString(run.Text)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// measureHeight reports how much vertical space an element will need without
// drawing it, so a layout can be planned before anything is emitted.
func (r *Renderer) measureHeight(g theme.Geometry, element, content string, maxWidth float64) float64 {
	st, ok := g.Text[element]
	if !ok || content == "" {
		return 0
	}
	if st.Uppercase {
		content = strings.ToUpper(content)
	}
	if r.StripEmoji {
		content = r.stripEmoji(st.Face, content)
	}
	block, _, err := r.Fonts.FitStyle(st, content, maxWidth)
	if err != nil {
		return 0
	}
	return block.Height()
}
