// Command promo generates speaker promo graphics and social copy for a Cloud
// Native Days conference, from the live program on the conference website.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
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
