package render

import (
	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/theme"
)

// landscape renders the 1200x630 OpenGraph-ratio card: photo and speaker block
// on the left, talk panel on the right, conference details along the top.
//
// The split mirrors the website's own speaker share card, so a promo posted as
// a link preview and one posted as an image look like siblings.
func (r *Renderer) landscape(conf cnd.Conference, t promo.Talk, g theme.Geometry, p *pass) (string, error) {
	c := &canvas{} // see portrait for why the content gets its own canvas

	pad := float64(g.Pad)
	width := float64(g.Width)
	height := float64(g.Height)

	// Top strip: date and location left, logo right.
	metaText := joinMeta(conf.DateRange(), conf.Location())
	metaH, err := r.text(c, p, g, "meta", metaText, pad, pad, width*0.55, "start")
	if err != nil {
		return "", err
	}
	headerBottom := pad + metaH

	if logoW, logoH, ok := logoBox(conf, g); ok {
		c.write(inlineSVG(conf.LogoBright, width-pad-logoW, pad-logoH*0.15, logoW, logoH))
		if bottom := pad - logoH*0.15 + logoH; bottom > headerBottom {
			headerBottom = bottom
		}
	}

	// Footer, placed early so both columns know their lower bound.
	footerH := r.measureHeight(g, "footer", conf.Domain, width*0.4)
	footerTop := height - pad - footerH
	if footerH > 0 {
		if _, err := r.text(c, p, g, "footer", conf.Domain, pad, footerTop, width*0.4, "start"); err != nil {
			return "", err
		}
	}

	bodyTop := headerBottom + float64(g.Gap)
	bodyBottom := footerTop - float64(g.Gap)*0.5

	// Left column: photo row, names, roles.
	leftWidth := width*0.38 - pad
	leftCentre := pad + leftWidth/2

	photoSize, positions := photoRow(g, len(t.Speakers), leftCentre, leftWidth)
	nameH := r.measureHeight(g, "name", t.SpeakerNames(), leftWidth)
	role := rolesLine(t.Speakers)
	roleH := r.measureHeight(g, "role", role, leftWidth)

	stackH := nameH + roleH
	if len(positions) > 0 {
		stackH += photoSize + float64(g.Gap)*0.6
	}
	y := bodyTop
	if available := bodyBottom - bodyTop; available > stackH {
		y = bodyTop + (available-stackH)/2
	}

	r.drawPhotos(c, p, t.Speakers, positions, y, photoSize, float64(g.Radius))
	if len(positions) > 0 {
		y += photoSize + float64(g.Gap)*0.6
	}
	if _, err := r.text(c, p, g, "name", t.SpeakerNames(), leftCentre, y, leftWidth, "middle"); err != nil {
		return "", err
	}
	y += nameH
	if roleH > 0 {
		if _, err := r.text(c, p, g, "role", role, leftCentre, y, leftWidth, "middle"); err != nil {
			return "", err
		}
	}

	// Right column: the talk panel, filling the remaining width.
	rightX := width*0.38 + float64(g.Gap)*0.5
	rightWidth := width - pad - rightX
	if err := r.talkPanel(c, p, g, t, rightX, bodyTop, bodyBottom, rightWidth); err != nil {
		return "", err
	}

	return r.prelude(g, p) + c.String() + "</svg>\n", nil
}
