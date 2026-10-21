package speech

import (
	"strings"
	"unicode/utf8"
)

func splitPhrases(text string, flush bool) ([]string, string) {
	var ready []string
	start := 0
	for i, r := range text {
		width := utf8.RuneLen(r)
		next := i + width
		boundary := r == '\n' || ((r == '.' || r == '?' || r == '!') && next < len(text) && (text[next] == ' ' || text[next] == '\n'))
		if boundary {
			if p := strings.TrimSpace(text[start:next]); p != "" {
				ready = append(ready, p)
			}
			start = next
		}
	}
	remaining := text[start:]
	if flush {
		if p := strings.TrimSpace(remaining); p != "" {
			ready = append(ready, p)
		}
		return ready, ""
	}
	return ready, remaining
}
