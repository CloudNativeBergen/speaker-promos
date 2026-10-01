package render

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/theme"
)

func TestParsePaint(t *testing.T) {
	cases := []struct {
		in      string
		color   string
		opacity float64
	}{
		// The form themes are written in, and the one that used to render black.
		{"rgba(255,255,255,0.13)", "#FFFFFF", 0.13},
		{"rgba(255, 255, 255, 0.88)", "#FFFFFF", 0.88},
		{"rgb(29,78,216)", "#1D4ED8", 1},
		{"#1D4ED8", "#1D4ED8", 1},
		{"#fff", "#ffffff", 1},
		{"#1D4ED880", "#1D4ED8", 128.0 / 255},
		{"white", "white", 1},
		{"none", "none", 1},
		{"transparent", "none", 1},
		{"", "none", 1},
		// Percentages and space-separated channels are legal CSS.
		{"rgba(100%,100%,100%,0.5)", "#FFFFFF", 0.5},
		{"rgb(255 255 255)", "#FFFFFF", 1},
		{"rgba(255 255 255 / 50%)", "#FFFFFF", 0.5},
		// Out-of-range values clamp rather than wrap.
		{"rgba(300,-20,255,2)", "#FF00FF", 1},
		// Unparseable input passes through: SVG accepts colour forms this does
		// not model, and blanking one the user typed would be worse.
		{"hsl(200 50% 50%)", "hsl(200 50% 50%)", 1},
	}
	for _, c := range cases {
		got := parsePaint(c.in)
		if !strings.EqualFold(got.Color, c.color) {
			t.Errorf("parsePaint(%q).Color = %q, want %q", c.in, got.Color, c.color)
		}
		if math.Abs(got.Opacity-c.opacity) > 0.005 {
			t.Errorf("parsePaint(%q).Opacity = %v, want %v", c.in, got.Opacity, c.opacity)
		}
	}
}

func TestPaintAttrs(t *testing.T) {
	// An opaque colour emits no opacity attribute at all.
	if got := parsePaint("#1D4ED8").fillAttrs(1); got != `fill="#1D4ED8"` {
		t.Errorf("fillAttrs = %q", got)
	}
	// A translucent colour splits into two attributes.
	if got := parsePaint("rgba(255,255,255,0.5)").fillAttrs(1); got != `fill="#FFFFFF" fill-opacity="0.5"` {
		t.Errorf("fillAttrs = %q", got)
	}
	if got := parsePaint("rgba(255,255,255,0.5)").strokeAttrs(1); got != `stroke="#FFFFFF" stroke-opacity="0.5"` {
		t.Errorf("strokeAttrs = %q", got)
	}
	// A style's own opacity multiplies the colour's alpha rather than replacing
	// it, so a 0.88 colour in a 0.5 style ends up at 0.44.
	if got := parsePaint("rgba(255,255,255,0.88)").fillAttrs(0.5); got != `fill="#FFFFFF" fill-opacity="0.44"` {
		t.Errorf("composed fillAttrs = %q", got)
	}
	// "none" never carries an opacity.
	if got := parsePaint("none").fillAttrs(0.3); got != `fill="none"` {
		t.Errorf("none fillAttrs = %q", got)
	}
}

func TestSVGBody(t *testing.T) {
	// viewBox is taken as-is when present.
	inner, vb, ok := svgBody(`<svg viewBox="0 0 100 50"><rect/></svg>`)
	if !ok || vb != "0 0 100 50" || inner != "<rect/>" {
		t.Errorf("svgBody = %q, %q, %v", inner, vb, ok)
	}

	// Without a viewBox, one is synthesised from width/height — otherwise the
	// logo cannot be scaled predictably.
	_, vb, ok = svgBody(`<svg width="2710" height="747" xmlns="http://www.w3.org/2000/svg"><path/></svg>`)
	if !ok || vb != "0 0 2710 747" {
		t.Errorf("synthesised viewBox = %q, %v", vb, ok)
	}

	// XML declarations, doctypes and comments are stripped.
	inner, _, ok = svgBody(`<?xml version="1.0"?><!-- c --><svg viewBox="0 0 1 1"><g/></svg>`)
	if !ok || strings.Contains(inner, "<!--") {
		t.Errorf("inner = %q", inner)
	}

	// script and foreignObject are removed: the markup comes from a CMS field
	// and a generated file that may be opened in a browser should not carry
	// executable content it does not need.
	inner, _, ok = svgBody(`<svg viewBox="0 0 1 1"><script>bad()</script><rect/><foreignObject><b/></foreignObject></svg>`)
	if !ok {
		t.Fatal("want ok")
	}
	if strings.Contains(inner, "script") || strings.Contains(inner, "foreignObject") {
		t.Errorf("inner still holds stripped elements: %q", inner)
	}
	if !strings.Contains(inner, "<rect/>") {
		t.Errorf("stripping removed wanted content: %q", inner)
	}

	// Unusable input is rejected rather than rendered at an arbitrary size.
	for _, bad := range []string{"", "not svg", `<svg><rect/></svg>`, `<svg width="10"><rect/></svg>`} {
		if _, _, ok := svgBody(bad); ok {
			t.Errorf("svgBody(%q) should have failed", bad)
		}
	}
}

func TestSVGAspect(t *testing.T) {
	if got, ok := svgAspect(`<svg viewBox="0 0 2710 747"><g/></svg>`); !ok || math.Abs(got-2710.0/747) > 0.001 {
		t.Errorf("svgAspect = %v, %v", got, ok)
	}
	// Comma-separated viewBox values are legal.
	if got, ok := svgAspect(`<svg viewBox="0,0,100,50"><g/></svg>`); !ok || math.Abs(got-2) > 0.001 {
		t.Errorf("comma viewBox aspect = %v, %v", got, ok)
	}
	for _, bad := range []string{"", `<svg viewBox="0 0 0 0"><g/></svg>`, `<svg viewBox="bad"><g/></svg>`} {
		if _, ok := svgAspect(bad); ok {
			t.Errorf("svgAspect(%q) should have failed", bad)
		}
	}
}

func TestInlineSVGNestsRatherThanSplices(t *testing.T) {
	// A nested <svg> establishes its own viewport, so the source keeps its own
	// coordinate system and preserveAspectRatio does the scaling.
	got := inlineSVG(`<svg viewBox="0 0 2710 747"><rect/></svg>`, 10, 20, 600, 165)
	for _, want := range []string{`x="10"`, `y="20"`, `width="600"`, `viewBox="0 0 2710 747"`, `preserveAspectRatio`, "<rect/>"} {
		if !strings.Contains(got, want) {
			t.Errorf("inlineSVG output missing %q:\n%s", want, got)
		}
	}
	if inlineSVG("garbage", 0, 0, 10, 10) != "" {
		t.Error("unusable source should produce nothing")
	}
}

func TestPhotoRowFitsWithinWidth(t *testing.T) {
	th := mustTheme(t)
	g, err := th.Size("portrait")
	if err != nil {
		t.Fatal(err)
	}
	const maxWidth = 1180
	const centre = 700

	if size, lefts := photoRow(g, 0, centre, maxWidth); size != 0 || lefts != nil {
		t.Errorf("zero speakers = %v, %v", size, lefts)
	}

	var prev float64
	for _, count := range []int{1, 2, 3, 4} {
		size, lefts := photoRow(g, count, centre, maxWidth)
		if len(lefts) != count {
			t.Fatalf("%d speakers gave %d positions", count, len(lefts))
		}
		// The row must fit the content width, and stay centred on it.
		left := lefts[0]
		right := lefts[len(lefts)-1] + size
		if width := right - left; width > maxWidth+0.01 {
			t.Errorf("%d speakers: row is %v px, over %v", count, width, maxWidth)
		}
		if mid := (left + right) / 2; math.Abs(mid-centre) > 0.01 {
			t.Errorf("%d speakers: row centred at %v, want %v", count, mid, centre)
		}
		// Photos must shrink as speakers are added, never grow.
		if prev != 0 && size >= prev {
			t.Errorf("%d speakers: size %v did not shrink from %v", count, size, prev)
		}
		prev = size
		// Positions must be strictly increasing and non-overlapping.
		for i := 1; i < len(lefts); i++ {
			if lefts[i] < lefts[i-1]+size {
				t.Errorf("%d speakers: photo %d overlaps its neighbour", count, i)
			}
		}
	}

	// Beyond four the row is capped rather than shrinking to unrecognisable.
	size, lefts := photoRow(g, 9, centre, maxWidth)
	if len(lefts) != 4 {
		t.Errorf("9 speakers gave %d positions, want the cap of 4", len(lefts))
	}
	if size <= 0 {
		t.Errorf("capped row size = %v", size)
	}
}

func TestRolesLine(t *testing.T) {
	for _, tc := range []struct {
		titles []string
		want   string
	}{
		{nil, ""},
		{[]string{""}, ""},
		{[]string{"Utvikler"}, "Utvikler"},
		{[]string{"Utvikler", "Naisutvikler"}, "Utvikler · Naisutvikler"},
		// One segment per speaker, even when two of them read the same: four
		// names over one role line gave no way to tell who it described.
		{[]string{"Bergsdal", "Bergsdal"}, "Bergsdal · Bergsdal"},
		{[]string{"Bergsdal", "bergsdal"}, "Bergsdal · bergsdal"},
		{[]string{"Utvikler hos Nav", "Utvikler hos Nav", "Naisutvikler", "Utvikler hos Nav-IT"},
			"Utvikler hos Nav · Utvikler hos Nav · Naisutvikler · Utvikler hos Nav-IT"},
		// Speakers with no title are skipped rather than leaving a stray dot.
		{[]string{"Utvikler", "", "Arkitekt"}, "Utvikler · Arkitekt"},
	} {
		if got := rolesLine(speakersWithTitles(tc.titles...)); got != tc.want {
			t.Errorf("rolesLine(%v) = %q, want %q", tc.titles, got, tc.want)
		}
	}
}

func TestJoinMeta(t *testing.T) {
	if got := joinMeta("a", "", "  ", "b"); got != "a · b" {
		t.Errorf("joinMeta = %q", got)
	}
	if got := joinMeta("", ""); got != "" {
		t.Errorf("joinMeta empty = %q", got)
	}
}

func mustTheme(t *testing.T) *theme.Theme {
	t.Helper()
	th, err := theme.Default()
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func speakersWithTitles(titles ...string) []promo.Speaker {
	out := make([]promo.Speaker, 0, len(titles))
	for i, title := range titles {
		out = append(out, promo.Speaker{Key: strconv.Itoa(i), Name: "Speaker " + strconv.Itoa(i), Title: title})
	}
	return out
}
