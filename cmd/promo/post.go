package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/lang"
	"github.com/vehagn/speaker-promos/internal/post"
	"github.com/vehagn/speaker-promos/internal/promo"
)

func cmdPost(args []string) error {
	fs := newFlagSet("post")
	var common commonFlags
	common.register(fs)
	all := fs.Bool("all", false, "draft copy for every talk")
	platform := fs.String("platform", "both", "linkedin, bluesky, or both")
	var manifestPath manifestFlag
	manifestPath.register(fs)
	noLinks := fs.Bool("no-links", false, "skip fetching speaker pages for social handles")
	language := fs.String("language", "auto", "copy language: auto, en or no")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	set, err := manifestPath.load()
	if err != nil {
		return err
	}
	copyLang, err := lang.ParseLanguage(*language)
	if err != nil {
		return err
	}

	loader := common.loader()
	program, err := loader.Load()
	if err != nil {
		return err
	}
	resolver := &promo.Resolver{
		Program:  program,
		Set:      set,
		Language: copyLang,
		Links: linkFetcher(loader, *noLinks, func(sp cnd.Speaker, err error) {
			fmt.Fprintf(os.Stderr, "note: could not read %s's profile page: %v\n", sp.Name, err)
		}),
	}
	talks, err := selectTalks(resolver, *all, fs.Args())
	if err != nil {
		return err
	}

	for i, t := range talks {
		if i > 0 {
			fmt.Println(strings.Repeat("─", 72))
		}
		in := post.Input{Conference: program.Conference, Talk: t}

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
	limit := ""
	if d.Platform == "bluesky" {
		limit = fmt.Sprintf("/%d", post.BlueskyLimit)
		if d.Runes() > post.BlueskyLimit {
			limit += " OVER LIMIT"
		}
	}
	fmt.Printf("\n── %s (%d%s chars) ──\n\n%s\n", d.Platform, d.Runes(), limit, d.Text)
	if len(d.Notes) > 0 {
		fmt.Println("\ncheck before posting:")
		for _, n := range d.Notes {
			fmt.Println("  - " + n)
		}
	}
}
