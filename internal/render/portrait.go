package render

import (
	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/promo"
	"github.com/vehagn/speaker-promos/internal/theme"
)

// portrait renders the 2:3 card: logo and date at the top, speakers in the
// middle, the talk in a panel below, conference domain at the foot.
//
// The stack is laid out top-down from measured heights rather than fixed
// offsets, because both the name block and the talk title autofit to between
// one and five lines. The talk panel is then centred in whatever vertical space
// is left between the speaker block and the footer, so a short title sits in
// the middle of its space rather than clinging to the top.
func (r *Renderer) portrait(conf cnd.Conference, t promo.Talk, g theme.Geometry, p *pass) (string, error) {
	// Content is drawn into its own canvas so the prelude — which must embed
	// exactly the font faces the content ends up using — can be composed once
	// those are known.
	c := &canvas{}

	pad := float64(g.Pad)
	width := float64(g.Width)
	height := float64(g.Height)
	content := width - 2*pad
	centre := width / 2
	y := pad

	// Conference wordmark, sized by width using its own aspect ratio.
	if logoW, logoH, ok := logoBox(conf, g); ok {
		c.write(inlineSVG(conf.LogoBright, centre-logoW/2, y, logoW, logoH))
		y += logoH + float64(g.Gap)*0.7
	} else {
		// No logo: fall back to the conference name so a card is never
		// unattributed.
		h, err := r.text(c, p, g, "name", conf.Title, centre, y, content, "middle")
		if err != nil {
			return "", err
		}
		y += h + float64(g.Gap)*0.7
	}

	// Date and place.
	h, err := r.text(c, p, g, "meta", joinMeta(conf.DateRange(), conf.Location()),
		centre, y, content, "middle")
	if err != nil {
		return "", err
	}
	y += h + float64(g.Gap)

	// Speaker photos in a row, shrinking as speakers are added so the row
	// always fits the content width.
	photoSize, positions := photoRow(g, len(t.Speakers), centre, content)
	r.drawPhotos(c, p, t.Speakers, positions, y, photoSize, float64(g.Radius))
	if len(positions) > 0 {
		y += photoSize + float64(g.Gap)*0.85
	}

	// Names, then roles.
	if h, err = r.text(c, p, g, "name", t.SpeakerNames(), centre, y, content, "middle"); err != nil {
		return "", err
	}
	y += h

	if role := rolesLine(t.Speakers); role != "" {
		if h, err = r.text(c, p, g, "role", role, centre, y, content, "middle"); err != nil {
			return "", err
		}
		y += h
	}

	// The footer is placed first so the talk panel knows how much room is left.
	footerH := r.measureHeight(g, "footer", conf.Domain, content)
	footerTop := height - pad - footerH
	if footerH > 0 {
		if _, err := r.text(c, p, g, "footer", conf.Domain, centre, footerTop, content, "middle"); err != nil {
			return "", err
		}
	}

	top := y + float64(g.Gap)
	bottom := footerTop - float64(g.Gap)
	if err := r.talkPanel(c, p, g, t, pad, top, bottom, content); err != nil {
		return "", err
	}

	return r.prelude(g, p) + c.String() + "</svg>\n", nil
}

// talkPanel draws the eyebrow, talk title and detail line inside a translucent
// panel, vertically centred between top and bottom.
func (r *Renderer) talkPanel(c *canvas, p *pass, g theme.Geometry, t promo.Talk, x, top, bottom, width float64) error {
	inner := width - float64(g.Gap)*2
	eyebrow := t.Schedule.Label()
	detail := talkDetail(t.Talk)

	eyebrowH := r.measureHeight(g, "eyebrow", eyebrow, inner)
	titleH := r.measureHeight(g, "talk", t.Title, inner)
	detailH := r.measureHeight(g, "meta", detail, inner)

	padV := float64(g.Gap) * 0.9
	gapS := float64(g.Gap) * 0.45
	boxH := padV*2 + eyebrowH + titleH
	if eyebrowH > 0 && titleH > 0 {
		boxH += gapS
	}
	if detailH > 0 {
		boxH += gapS + detailH
	}

	boxY := top
	if available := bottom - top; available > boxH {
		boxY = top + (available-boxH)/2
	}

	c.writef("  <rect x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\" rx=\"%s\" %s %s stroke-width=\"2\"/>\n",
		num(x), num(boxY), num(width), num(boxH), num(float64(g.Radius)),
		parsePaint(r.Theme.Palette.CardFill).fillAttrs(1),
		parsePaint(r.Theme.Palette.CardStroke).strokeAttrs(1))

	centre := x + width/2
	y := boxY + padV

	if eyebrowH > 0 {
		h, err := r.text(c, p, g, "eyebrow", eyebrow, centre, y, inner, "middle")
		if err != nil {
			return err
		}
		y += h + gapS
	}
	if titleH > 0 {
		h, err := r.text(c, p, g, "talk", t.Title, centre, y, inner, "middle")
		if err != nil {
			return err
		}
		y += h
	}
	if detailH > 0 {
		if _, err := r.text(c, p, g, "meta", detail, centre, y+gapS, inner, "middle"); err != nil {
			return err
		}
	}
	return nil
}

// talkDetail is the format and level line under the title.
func talkDetail(t cnd.Talk) string {
	return joinMeta(t.FormatLabel(), t.LevelLabel())
}
