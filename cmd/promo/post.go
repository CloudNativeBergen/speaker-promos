package main

import (
	"fmt"
	"strings"

	"github.com/vehagn/speaker-promos/internal/post"
)

func cmdPost(args []string) error {
	fs := newFlagSet("post")
	var pf projectFlags
	var cf copyFlags
	pf.register(fs)
	cf.register(fs)
	all := fs.Bool("all", false, "draft copy for every talk")
	platform := fs.String("platform", "both", "linkedin, bluesky, or both")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	p, err := pf.open()
	if err != nil {
		return err
	}
	resolver, err := p.resolver(cf)
	if err != nil {
		return err
	}
	talks, err := selectTalks(resolver, *all, fs.Args())
	if err != nil {
		return err
	}

	for i, t := range talks {
		if i > 0 {
			fmt.Println(strings.Repeat("─", 72))
		}
		in := post.Input{Conference: p.program.Conference, Talk: t}

		var drafts []post.Draft
		switch *platform {
		case "linkedin":
			drafts = []post.Draft{post.LinkedIn(in)}
		case "bluesky":
			drafts = []post.Draft{post.Bluesky(in)}
		case "both", "all":
			drafts = []post.Draft{post.LinkedIn(in), post.Bluesky(in)}
		default:
			return fmt.Errorf("unknown platform %q (want linkedin, bluesky or both)", *platform)
		}

		for _, d := range drafts {
			printDraft(d)
		}
		if mentions := post.Mentions(in); len(mentions) > 0 {
			fmt.Println("\nprofiles to mention:")
			for _, m := range mentions {
				fmt.Println("  " + m)
			}
		}
	}
	return nil
}

func printDraft(d post.Draft) {
	over := ""
	if d.Over() {
		over = " OVER LIMIT"
	}
	fmt.Printf("\n── %s (%s chars%s) ──\n\n%s\n", d.Platform, d.Count(), over, d.Text)
	if len(d.Notes) > 0 {
		fmt.Println("\ncheck before posting:")
		for _, n := range d.Notes {
			fmt.Println("  - " + n)
		}
	}
}
