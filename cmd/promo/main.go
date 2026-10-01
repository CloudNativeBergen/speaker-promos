// Command promo generates speaker promo graphics and social copy for a Cloud
// Native Days conference, from the live program on the conference website.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vehagn/speaker-promos/internal/cache"
	"github.com/vehagn/speaker-promos/internal/cnd"
	"github.com/vehagn/speaker-promos/internal/manifest"
	"github.com/vehagn/speaker-promos/internal/promo"
)

const usage = `promo — speaker promo graphics for Cloud Native Days

Usage:
  promo list [flags]                 index the program
  promo export [flags] <selector>... write a folder per talk: cards, copy, config
  promo import [flags] <path>...     merge edited promo.yaml files back
  promo post [flags] <selector>      draft LinkedIn / Bluesky copy
  promo serve [flags]                preview every card in a browser and edit
  promo fonts install                install the brand fonts locally
  promo theme dump                   print the built-in theme as YAML

A <selector> picks talks by id prefix, speaker slug, or a substring of the
talk title, e.g. "dario-haaland" or "nok nett". Use --all to
select every talk.

Run "promo <command> -h" for the flags of a command.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "promo:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "list":
		return cmdList(rest)
	case "export":
		return cmdExport(rest)
	case "import":
		return cmdImport(rest)
	case "post":
		return cmdPost(rest)
	case "serve":
		return cmdServe(rest)
	case "fonts":
		return cmdFonts(rest)
	case "theme":
		return cmdTheme(rest)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
}

// commonFlags are the data-source flags every program-reading command shares.
type commonFlags struct {
	domain  string
	ttl     time.Duration
	noCache bool
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.domain, "domain", cnd.DefaultDomain, "conference site to read the program from")
	fs.DurationVar(&c.ttl, "cache-ttl", 6*time.Hour, "how long a cached page stays fresh")
	fs.BoolVar(&c.noCache, "no-cache", false, "always re-fetch, ignoring the cache")
}

// loader reads the program and the speaker pages, honouring --domain and the
// cache flags.
func (c *commonFlags) loader() *cnd.Loader {
	loader := cnd.NewLoader(c.domain, c.ttl)
	loader.Cache.Disabled = c.noCache
	return loader
}

func (c *commonFlags) load() (*cnd.Program, error) {
	return c.loader().Load()
}

// photoCache is the cache speaker photos are fetched through, or nil for a run
// with --no-photos, which is what makes the cards fall back to initials.
//
// Photos are cached far longer than the program: a portrait does not change
// between runs, and each one is a separate CDN fetch.
func (c *commonFlags) photoCache(enabled bool) *cache.Cache {
	if !enabled {
		return nil
	}
	images := cache.New(30 * 24 * time.Hour)
	images.Disabled = c.noCache
	return images
}

// manifestFlag registers the override-manifest path for a command that honours
// overrides, and loads it.
type manifestFlag struct{ path string }

func (m *manifestFlag) register(fs *flag.FlagSet) {
	// No backticks in the usage text: the flag package reads those as the
	// value-name placeholder and would print "-manifest promo serve".
	fs.StringVar(&m.path, "manifest", manifest.DefaultPath,
		"override manifest of employer and title corrections")
}

func (m *manifestFlag) load() (*manifest.Set, error) {
	return manifest.Load(m.path)
}

// selectTalks resolves positional selectors, or every talk with --all, with
// the manifest's corrections applied — and says so when --all skipped hidden
// talks, since a quietly shorter export is easy to miss.
func selectTalks(r *promo.Resolver, all bool, selectors []string) ([]promo.Talk, error) {
	talks, skipped, err := r.Select(all, selectors)
	if err != nil {
		return nil, err
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "note: skipping %d talk(s) marked hidden in %s\n", skipped, r.Set.Path())
	}
	return talks, nil
}

// linkFetcher is the Resolver.Links callback for a CLI run: it fetches each
// speaker page at most once, since a speaker on two talks would otherwise be
// fetched twice and each page is around 3 MB. Nil with --no-links.
//
// warn, when set, is told about a page that could not be read; handles only
// suggest mentions, so a failure never aborts the run.
func linkFetcher(loader *cnd.Loader, skip bool, warn func(cnd.Speaker, error)) func(cnd.Speaker) cnd.Links {
	if skip {
		return nil
	}
	seen := map[string]cnd.Links{}
	return func(sp cnd.Speaker) cnd.Links {
		// A speaker page is addressed by slug, so a speaker without one simply
		// has no profile to scrape.
		if sp.Slug == "" {
			return cnd.Links{}
		}
		if l, ok := seen[sp.Key()]; ok {
			return l
		}
		l, err := loader.SpeakerLinks(sp)
		if err != nil && warn != nil {
			warn(sp, err)
		}
		seen[sp.Key()] = l
		return l
	}
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("promo "+name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s\n\nFlags:\n",
			strings.TrimSpace("promo "+name+" [flags] "+positionalHint(name)))
		fs.PrintDefaults()
	}
	return fs
}

func positionalHint(cmd string) string {
	switch cmd {
	case "export":
		return "<selector>..."
	case "import":
		return "<path>..."
	case "post":
		return "<selector>"
	default:
		return ""
	}
}

// truncate shortens s to at most n runes for tabular output.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
}

// parseFlags parses args allowing flags to appear after positional arguments.
//
// Go's flag package stops parsing at the first non-flag argument, so
// `promo export dario-haaland --out promos/` would silently treat "--out" and
// "promos/" as selectors. That word order is the natural one and every other
// modern CLI accepts it, so the arguments are permuted first: flags (with their
// values) are hoisted ahead of the positionals.
func parseFlags(fs *flag.FlagSet, args []string) error {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]

		// A bare "--" ends flag parsing; everything after it is positional.
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}

		flags = append(flags, a)
		name, inlineValue := cutFlagValue(strings.TrimLeft(a, "-"))
		if inlineValue {
			continue
		}
		// A non-boolean flag takes the following argument as its value, so that
		// argument must travel with it rather than becoming a positional.
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return fs.Parse(append(flags, positional...))
}

// cutFlagValue splits "name=value" and reports whether a value was inline.
func cutFlagValue(s string) (string, bool) {
	if name, _, ok := strings.Cut(s, "="); ok {
		return name, true
	}
	return s, false
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}
