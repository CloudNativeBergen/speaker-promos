package web

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/vehagn/speaker-promos/internal/theme"
)

// control is one card slider or colour, as it appears both in the global
// settings and on each talk: one table drives the inputs and reads them back.
type control struct {
	name, label    string
	min, max, step float64 // zero step: a colour
	of             func(*theme.Adjust) any
}

var cardControls = []control{
	{"titleScale", "Title size", theme.ScaleMin, theme.ScaleMax, 0.05, func(a *theme.Adjust) any { return &a.TitleScale }},
	{"nameScale", "Name size", theme.ScaleMin, theme.ScaleMax, 0.05, func(a *theme.Adjust) any { return &a.NameScale }},
	{"photoScale", "Photo size", theme.ScaleMin, theme.ScaleMax, 0.05, func(a *theme.Adjust) any { return &a.PhotoScale }},
	{"spacing", "Spacing", theme.ScaleMin, theme.ScaleMax, 0.05, func(a *theme.Adjust) any { return &a.Spacing }},
	{"gradientFrom", "Gradient from", 0, 0, 0, func(a *theme.Adjust) any { return &a.GradientFrom }},
	{"gradientTo", "Gradient to", 0, 0, 0, func(a *theme.Adjust) any { return &a.GradientTo }},
}

func (c control) get(a theme.Adjust) string {
	switch v := c.of(&a).(type) {
	case *float64:
		return strconv.FormatFloat(*v, 'f', -1, 64)
	case *string:
		return *v
	}
	return ""
}

// same compares a submitted value with one formatted by get.
func (c control) same(a, b string) bool {
	if c.step == 0 {
		return strings.EqualFold(a, b)
	}
	x, err1 := strconv.ParseFloat(a, 64)
	y, err2 := strconv.ParseFloat(b, 64)
	return err1 == nil && err2 == nil && math.Abs(x-y) < 1e-9
}

func (c control) set(a *theme.Adjust, s string) {
	switch v := c.of(a).(type) {
	case *float64:
		*v, _ = strconv.ParseFloat(s, 64)
	case *string:
		*v = s
	}
}

// neutral is every control at the value that changes nothing: scales of 1 and
// the theme's own colours.
func (s *Server) neutral() theme.Adjust {
	p := s.renderer.Theme.Palette
	return theme.Adjust{TitleScale: 1, NameScale: 1, PhotoScale: 1, Spacing: 1,
		GradientFrom: p.GradientFrom, GradientTo: p.GradientTo}
}

// controls renders the card controls set to effective, their names led by
// prefix so a talk's ("card.") cannot be confused with the form's own fields.
func controls(effective theme.Adjust, prefix, anchor string) []input {
	out := make([]input, 0, len(cardControls))
	for _, c := range cardControls {
		in := input{Label: c.label, Name: prefix + c.name, Value: c.get(effective), Anchor: anchor}
		if c.step == 0 {
			in.Type = "color"
		} else {
			in.Type, in.Min, in.Max, in.Step = "range", c.min, c.max, c.step
		}
		out = append(out, in)
	}
	return out
}

// readAdjust reads the card controls back from a form. A value equal to what
// would be inherited anyway is not an adjustment and is left unset, so moving a
// slider back to where it started stores nothing.
func readAdjust(r *http.Request, prefix string, inherited theme.Adjust) theme.Adjust {
	var out theme.Adjust
	if r.FormValue("reset") != "" {
		return out
	}
	for _, c := range cardControls {
		v := strings.TrimSpace(r.FormValue(prefix + c.name))
		if v != "" && !c.same(v, c.get(inherited)) {
			c.set(&out, v)
		}
	}
	return out
}
