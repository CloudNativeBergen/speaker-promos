package render

import (
	"sort"
	"strings"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/theme"
)

// ascent approximates the distance from the top of a line to its baseline.
//
// 0.78 of the font size is a close-enough stand-in for the ascent of the
// bundled faces. Using the real hhea ascender would be more precise but it also
// reserves accent clearance, which leaves visible dead space above lines that
// only reach cap height — most of the text on a card.
func ascent(size float64) float64 { return size * 0.78 }

// photoRow computes the photo size and left edges for a row of speakers,
// centred on cx within maxWidth.
//
// Talks with several speakers get smaller photos so the row still fits: two
// speakers at full size would overflow the content column. Beyond four the row
// is capped, since the photos would be too small to recognise anyone.
func photoRow(g theme.Geometry, count int, cx, maxWidth float64) (size float64, lefts []float64) {
	if count <= 0 {
		return 0, nil
	}
	const maxShown = 4
	if count > maxShown {
		count = maxShown
	}

	size = float64(g.PhotoSize)
	gap := float64(g.Gap) * 0.4
	switch count {
	case 1:
	case 2:
		size *= 0.72
	case 3:
		size *= 0.55
	default:
		size *= 0.44
	}

	// Shrink further if the row still would not fit.
	total := float64(count)*size + float64(count-1)*gap
	if total > maxWidth {
		scale := maxWidth / total
		size *= scale
		gap *= scale
		total = maxWidth
	}

	x := cx - total/2
	for range count {
		lefts = append(lefts, x)
		x += size + gap
	}
	return size, lefts
}

// logoBox is the box the conference wordmark is drawn in: the theme's logo
// width, and the height the logo's own aspect ratio implies.
//
// ok is false when the markup carries no usable dimensions, which is what makes
// a card fall back to spelling the conference name out instead.
func logoBox(conf cnd.Conference, g theme.Geometry) (w, h float64, ok bool) {
	aspect, ok := svgAspect(conf.LogoBright)
	if !ok {
		return 0, 0, false
	}
	w = float64(g.LogoWidth)
	return w, w / aspect, true
}

// rolesLine renders the speakers' profile titles as one line, one segment per
// speaker in the order the names above them are listed.
//
// Repetition is deliberate: collapsing colleagues who share an employer into
// one segment leaves four names over one role, with no telling whose it is.
// A blank title contributes nothing rather than a stray separator, and when
// nobody has one the line is omitted.
func rolesLine(speakers []promo.Speaker) string {
	var out []string
	for _, sp := range speakers {
		if t := strings.TrimSpace(sp.Title); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, " · ")
}

// joinMeta joins non-empty parts with a middot separator.
func joinMeta(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

// sortFaces orders embedded faces so output bytes are stable across runs.
func sortFaces(faces []embeddedFace) {
	sort.Slice(faces, func(i, j int) bool {
		if faces[i].Family != faces[j].Family {
			return faces[i].Family < faces[j].Family
		}
		if faces[i].Weight != faces[j].Weight {
			return faces[i].Weight < faces[j].Weight
		}
		return faces[i].Style < faces[j].Style
	})
}
