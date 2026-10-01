package theme

import (
	"cmp"
	"fmt"
	"maps"
)

// Adjust is a handful of knobs laid over a theme for one card: the sliders in
// `promo serve`. Each zero field leaves the theme as it is, so the zero Adjust
// draws exactly the theme.
//
// They are deliberately few and coarse — scales and two colours rather than
// every number a theme has — because a slider is for nudging a card that
// almost fits, and a theme file is still there for a redesign.
type Adjust struct {
	// TitleScale and NameScale multiply the talk title's and the speaker
	// names' type sizes, autofit range and all.
	TitleScale float64 `yaml:"titleScale,omitempty"`
	NameScale  float64 `yaml:"nameScale,omitempty"`
	// PhotoScale multiplies the speaker photos' size.
	PhotoScale float64 `yaml:"photoScale,omitempty"`
	// Spacing multiplies the space between blocks.
	Spacing float64 `yaml:"spacing,omitempty"`
	// GradientFrom and GradientTo replace the backdrop's colours.
	GradientFrom string `yaml:"gradientFrom,omitempty"`
	GradientTo   string `yaml:"gradientTo,omitempty"`
}

// ScaleMin and ScaleMax bound every scale.
const ScaleMin, ScaleMax = 0.5, 2.0

// Over returns a with base's values wherever a has none: a talk's own
// adjustments over the global ones.
func (a Adjust) Over(base Adjust) Adjust {
	return Adjust{
		TitleScale:   cmp.Or(a.TitleScale, base.TitleScale),
		NameScale:    cmp.Or(a.NameScale, base.NameScale),
		PhotoScale:   cmp.Or(a.PhotoScale, base.PhotoScale),
		Spacing:      cmp.Or(a.Spacing, base.Spacing),
		GradientFrom: cmp.Or(a.GradientFrom, base.GradientFrom),
		GradientTo:   cmp.Or(a.GradientTo, base.GradientTo),
	}
}

// Validate rejects a scale outside ScaleMin..ScaleMax.
func (a Adjust) Validate() error {
	for name, v := range map[string]float64{
		"titleScale": a.TitleScale, "nameScale": a.NameScale,
		"photoScale": a.PhotoScale, "spacing": a.Spacing,
	} {
		if v != 0 && (v < ScaleMin || v > ScaleMax) {
			return fmt.Errorf("%s %g is out of range (%g–%g)", name, v, ScaleMin, ScaleMax)
		}
	}
	return nil
}

// Adjusted returns the theme with a applied, or the theme itself when a sets
// nothing. The theme is not modified.
func (t *Theme) Adjusted(a Adjust) *Theme {
	if a == (Adjust{}) {
		return t
	}
	out := *t
	out.Palette.GradientFrom = cmp.Or(a.GradientFrom, t.Palette.GradientFrom)
	out.Palette.GradientTo = cmp.Or(a.GradientTo, t.Palette.GradientTo)
	out.Geometry = make(map[string]Geometry, len(t.Geometry))
	for name, g := range t.Geometry {
		g.Gap = scaleInt(g.Gap, a.Spacing)
		g.PhotoSize = scaleInt(g.PhotoSize, a.PhotoScale)
		g.Text = maps.Clone(g.Text)
		scaleText(g.Text, "talk", a.TitleScale)
		scaleText(g.Text, "name", a.NameScale)
		out.Geometry[name] = g
	}
	return &out
}

func scaleInt(v int, by float64) int {
	if by == 0 {
		return v
	}
	return int(float64(v)*by + 0.5)
}

func scaleText(styles map[string]TextStyle, element string, by float64) {
	st, ok := styles[element]
	if !ok || by == 0 {
		return
	}
	st.MinSize *= by
	st.MaxSize *= by
	styles[element] = st
}
