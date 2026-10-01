package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/vehagn/speaker-promos/internal/export"
	"github.com/vehagn/speaker-promos/internal/progress"
	"github.com/vehagn/speaker-promos/internal/theme"
	"github.com/vehagn/speaker-promos/internal/web"
)

func cmdServe(args []string) error {
	fs := newFlagSet("serve")
	var common commonFlags
	common.register(fs)
	var manifestPath manifestFlag
	manifestPath.register(fs)
	addr := fs.String("addr", "localhost:8787", "address to listen on")
	size := fs.String("size", "portrait", "card size to preview")
	themePath := fs.String("theme", "", "theme YAML to merge over the built-in theme")
	out := fs.String("out", "out", "directory the Export button writes into")
	formats := fs.String("formats", "svg,png,jpg", "formats the Export button writes: any of svg, png, jpg")
	width := fs.Int("width", 0, "raster width in pixels (default: the card's own width)")
	quality := fs.Int("jpeg-quality", 88, "JPEG quality, 1-100")
	noPhotos := fs.Bool("no-photos", false, "skip speaker photos (renders initials instead)")
	noLinks := fs.Bool("no-links", false, "use the handles last recorded in the snapshot rather than fetching speaker pages")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	th, err := theme.Load(*themePath)
	if err != nil {
		return err
	}
	set, err := manifestPath.load()
	if err != nil {
		return err
	}
	loader := common.loader()
	program, snap, err := common.load(loader)
	if err != nil {
		return err
	}
	images := common.photoCache(!*noPhotos)

	wantFormats, err := export.ParseFormats(*formats)
	if err != nil {
		return err
	}

	server, err := web.New(web.Options{
		Program:     program,
		Set:         set,
		Source:      snap,
		Theme:       th,
		Images:      images,
		Loader:      loader,
		Size:        *size,
		OutDir:      *out,
		NoLinks:     *noLinks,
		Formats:     wantFormats,
		RasterWidth: *width,
		JPEGQuality: *quality,
	})
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
	fmt.Printf("%s — %d talks\n", program.Conference.Title, len(program.Talks))
	fmt.Printf("overrides: %s\n", set.Path())
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
