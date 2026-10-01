// Package progress reports how far a batch has got.
//
// Exporting or importing a whole program takes long enough — rasterising 40
// cards is minutes — that silence until the end reads as a hang. The batch
// functions take a Func, so the CLI can draw a bar and the preview server can
// fill a <progress> element from the same calls.
package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

// Func is told about each step: done of total finished, label naming the one
// just finished. A nil Func is valid and ignores everything.
type Func func(done, total int, label string)

// Report calls f if it is set.
func (f Func) Report(done, total int, label string) {
	if f != nil {
		f(done, total, label)
	}
}

// Bar draws a one-line bar on w, redrawn in place with \r:
//
//	[########------------]  12/40  d1-0900-imma-valls…
//
// It draws only when w is a terminal, so a piped or redirected run — and every
// test — gets no control characters in its output. Clear erases the line,
// which a caller does before printing anything else, and the bar clears itself
// once the last step is reported.
type Bar struct {
	mu      sync.Mutex
	w       io.Writer
	enabled bool
	drawn   bool
}

// NewBar returns a bar on w, live only if w is a terminal.
func NewBar(w io.Writer) *Bar {
	f, ok := w.(*os.File)
	enabled := false
	if ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			enabled = true
		}
	}
	return &Bar{w: w, enabled: enabled}
}

const width = 24

// Func returns the bar as a progress.Func.
func (b *Bar) Func() Func {
	return func(done, total int, label string) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if !b.enabled || total <= 0 {
			return
		}
		filled := done * width / total
		if utf8.RuneCountInString(label) > 40 {
			label = string([]rune(label)[:39]) + "…"
		}
		fmt.Fprintf(b.w, "\r\033[K[%s%s] %3d/%d  %s",
			strings.Repeat("#", filled), strings.Repeat("-", width-filled), done, total, label)
		b.drawn = true
		if done >= total {
			b.clear()
		}
	}
}

// Clear erases the bar so ordinary output can be printed on its line.
func (b *Bar) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clear()
}

func (b *Bar) clear() {
	if b.drawn {
		fmt.Fprint(b.w, "\r\033[K")
		b.drawn = false
	}
}
