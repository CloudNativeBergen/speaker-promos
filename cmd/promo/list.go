package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/vehagn/speaker-promos/internal/cnd"
)

func cmdList(args []string) error {
	fs := newFlagSet("list")
	var pf projectFlags
	pf.register(fs)
	day := fs.Int("day", 0, "show only this conference day (1-based)")
	speaker := fs.String("speaker", "", "show only talks by this speaker (slug or name substring)")
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	p, err := pf.open()
	if err != nil {
		return err
	}
	program, snap, set := p.program, p.snap, p.set

	q := strings.ToLower(*speaker)
	talks := slices.DeleteFunc(slices.Clone(program.Talks), func(t cnd.Talk) bool {
		if *day > 0 && t.Schedule.Day != *day {
			return true
		}
		return q != "" && !slices.ContainsFunc(t.Speakers, func(sp cnd.Speaker) bool {
			return strings.EqualFold(sp.Slug, q) || strings.Contains(strings.ToLower(sp.Name), q)
		})
	})

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(talks)
	}

	c := program.Conference
	fmt.Printf("%s — %s, %s\n", c.Title, c.DateRange(), c.Location())
	fmt.Printf("%d talks, %d speakers\n", len(talks), len(cnd.Program{Talks: talks}.Speakers()))
	fmt.Printf("website data fetched %s, last changed %s (%s)\n\n",
		program.FetchedAt.Local().Format("2 Jan 15:04"), snap.UpdatedAt().Local().Format("2 Jan 15:04"), snap.Path())

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "DAY\tTIME\tTRACK\tSPEAKERS\tTALK\tPOSTED\tSELECTOR")
	for _, t := range talks {
		spec, _ := set.Talk(t.ID)
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			t.Schedule.Day,
			t.Schedule.TimeRange(),
			truncate(t.Schedule.ShortTrack(), 18),
			truncate(t.SpeakerNames("and"), 28),
			truncate(t.Title, 44),
			cmp.Or(spec.Posted, "-"),
			primarySelector(t),
		)
	}
	return w.Flush()
}

// primarySelector is the shortest thing the user can copy to select a talk
// again: its first speaker's slug, falling back to a talk id prefix.
func primarySelector(t cnd.Talk) string {
	for _, sp := range t.Speakers {
		if sp.Slug != "" {
			return sp.Slug
		}
	}
	if len(t.ID) >= 8 {
		return t.ID[:8]
	}
	return t.ID
}
