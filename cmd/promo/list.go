package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/vehagn/speaker-promos/internal/cnd"
)

func cmdList(args []string) error {
	fs := newFlagSet("list")
	var common commonFlags
	common.register(fs)
	day := fs.Int("day", 0, "show only this conference day (1-based)")
	speaker := fs.String("speaker", "", "show only talks by this speaker (slug or name substring)")
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	var manifestPath manifestFlag
	manifestPath.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	set, err := manifestPath.load()
	if err != nil {
		return err
	}
	program, snap, err := common.load(common.loader())
	if err != nil {
		return err
	}

	talks := program.Talks
	if *day > 0 {
		talks = filter(talks, func(t cnd.Talk) bool { return t.Schedule.Day == *day })
	}
	if *speaker != "" {
		q := strings.ToLower(*speaker)
		talks = filter(talks, func(t cnd.Talk) bool {
			for _, sp := range t.Speakers {
				if strings.EqualFold(sp.Slug, q) || strings.Contains(strings.ToLower(sp.Name), q) {
					return true
				}
			}
			return false
		})
	}

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
		posted := "-"
		if spec, ok := set.Talk(t.ID); ok && spec.Posted != "" {
			posted = spec.Posted
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			t.Schedule.Day,
			t.Schedule.TimeRange(),
			truncate(t.Schedule.ShortTrack(), 18),
			truncate(t.SpeakerNames("and"), 28),
			truncate(t.Title, 44),
			posted,
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

func filter(in []cnd.Talk, keep func(cnd.Talk) bool) []cnd.Talk {
	var out []cnd.Talk
	for _, s := range in {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}
