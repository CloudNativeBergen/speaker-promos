package layout

import "strings"

// Block is text laid out into lines at a chosen size.
type Block struct {
	Lines    []string
	Size     float64
	Tracking float64
	// LineHeight is the baseline-to-baseline distance in pixels.
	LineHeight float64
	// Overflow is true when the text could not be made to fit even at the
	// smallest allowed size, so the caller knows the result is a best effort.
	Overflow bool
}

// Height is the total vertical space the block occupies.
func (b Block) Height() float64 {
	if len(b.Lines) == 0 {
		return 0
	}
	return float64(len(b.Lines)) * b.LineHeight
}

// Wrap breaks text into lines no wider than maxWidth at the given size.
//
// Words longer than the line are split mid-word rather than allowed to
// overflow; a single unbroken token (a URL, or a long compound Norwegian noun)
// would otherwise run off the card.
func Wrap(f *Font, text string, size, tracking, maxWidth float64) []string {
	// Honour explicit newlines as hard breaks so a caller can force a layout.
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		lines = append(lines, wrapParagraph(f, para, size, tracking, maxWidth)...)
	}
	return lines
}

func wrapParagraph(f *Font, text string, size, tracking, maxWidth float64) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}

	var lines []string
	current := ""
	for _, word := range words {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if f.Measure(candidate, size, tracking) <= maxWidth {
			current = candidate
			continue
		}
		if current != "" {
			lines = append(lines, current)
			current = ""
		}
		// The word alone may still be too wide.
		if f.Measure(word, size, tracking) <= maxWidth {
			current = word
			continue
		}
		chunks := breakWord(f, word, size, tracking, maxWidth)
		lines = append(lines, chunks[:len(chunks)-1]...)
		current = chunks[len(chunks)-1]
	}
	if current != "" {
		lines = append(lines, current)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// breakWord splits an over-wide word into pieces that fit, returning at least
// one piece. The last piece is left for the caller to continue filling.
func breakWord(f *Font, word string, size, tracking, maxWidth float64) []string {
	var out []string
	current := ""
	for _, r := range word {
		candidate := current + string(r)
		if current != "" && f.Measure(candidate, size, tracking) > maxWidth {
			out = append(out, current)
			current = string(r)
			continue
		}
		current = candidate
	}
	return append(out, current)
}

// Fit lays text out at the largest size in [minSize, maxSize] whose wrap fits
// within maxLines, stepping down in whole pixels.
//
// When even minSize needs more lines than allowed, the text is wrapped at
// minSize, truncated to maxLines with an ellipsis on the last line, and
// Overflow is set. Truncating is the lesser evil: an overlapping or clipped
// block looks broken, whereas a shortened title still reads.
func Fit(f *Font, text string, minSize, maxSize, tracking, lineHeight, maxWidth float64, maxLines int) Block {
	if maxLines < 1 {
		maxLines = 1
	}
	if minSize <= 0 {
		minSize = maxSize
	}
	if maxSize < minSize {
		maxSize = minSize
	}

	for size := maxSize; size >= minSize; size-- {
		lines := Wrap(f, text, size, tracking, maxWidth)
		if len(lines) <= maxLines {
			return Block{Lines: lines, Size: size, Tracking: tracking, LineHeight: size * lineHeight}
		}
	}

	lines := Wrap(f, text, minSize, tracking, maxWidth)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ellipsize(f, lines[maxLines-1], minSize, tracking, maxWidth)
	}
	return Block{
		Lines:      lines,
		Size:       minSize,
		Tracking:   tracking,
		LineHeight: minSize * lineHeight,
		Overflow:   true,
	}
}

// ellipsize appends an ellipsis to a line, trimming runes until it fits.
func ellipsize(f *Font, line string, size, tracking, maxWidth float64) string {
	const ell = "…"
	runes := []rune(strings.TrimRight(line, " "))
	for len(runes) > 0 {
		candidate := strings.TrimRight(string(runes), " ") + ell
		if f.Measure(candidate, size, tracking) <= maxWidth {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return ell
}
