package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/vehagn/speaker-promos/internal/progress"
	"github.com/vehagn/speaker-promos/internal/web"
)

func cmdServe(args []string) error {
	fs := newFlagSet("serve")
	var pf projectFlags
	var cf copyFlags
	var kf cardFlags
	pf.register(fs)
	cf.register(fs)
	kf.register(fs)
	addr := fs.String("addr", "localhost:8787", "address to listen on")
	size := fs.String("size", "portrait", "card size to preview")
	out := fs.String("out", "out", "directory the Export button writes into")
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
	exporter, err := p.exporter(kf, resolver)
	if err != nil {
		return err
	}
	server, err := web.New(web.Options{Exporter: exporter, Size: *size, OutDir: *out})
	if err != nil {
		return err
	}

	// Bind before announcing, so the printed URL is never a lie and the
	// "address in use" case fails here rather than after the banner.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", *addr, err)
	}

	// Fetching profiles and photos is the slow part — 49 speaker pages at ~3 MB
	// each — so it happens here with a progress line rather than inside the
	// first page load, where it looked like a hung browser.
	fmt.Fprintln(os.Stderr, "fetching speaker profiles and photos…")
	bar := progress.NewBar(os.Stderr)
	report := bar.Func()
	server.Warm(6, func(done, total int) { report(done, total, "") })
	bar.Clear()
	fmt.Printf("%s — %d talks\n", p.program.Conference.Title, len(p.program.Talks))
	fmt.Printf("overrides: %s\n", p.set.Path())
	fmt.Printf("\n  http://%s\n\n", ln.Addr())
	fmt.Println("Edits save immediately. Ctrl-C to stop.")

	srv := &http.Server{
		Handler: server.Handler(),
		// Cards are ~700 KB each and a page asks for many at once, so the
		// write timeout has to be generous even on localhost.
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      2 * time.Minute,
	}
	return srv.Serve(ln)
}
