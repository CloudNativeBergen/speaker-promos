package post

import "strings"

func isAlnum(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// trimTo shortens text to at most n runes, cutting at a word boundary.
func trimTo(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		// Nothing useful fits; an ellipsis alone is one rune.
		if n == 1 {
			return "…"
		}
		return ""
	}
	cut := string(r[:n-1])
	if i := strings.LastIndexAny(cut, " \n"); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " \n") + "…"
}
