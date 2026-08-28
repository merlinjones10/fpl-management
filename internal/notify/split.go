package notify

import "strings"

// splitMessage breaks a body into chunks under the rune limit, preferring line
// boundaries so a table never splits mid-row.
func splitMessage(body string, limit int) []string {
	// Trim here as well as in flush below, so a body that fits and one that is
	// split are treated the same way.
	if len([]rune(body)) <= limit {
		return []string{strings.TrimRight(body, "\n")}
	}

	var parts []string
	var current strings.Builder
	currentLen := 0

	flush := func() {
		if currentLen > 0 {
			parts = append(parts, strings.TrimRight(current.String(), "\n"))
			current.Reset()
			currentLen = 0
		}
	}

	for _, line := range strings.SplitAfter(body, "\n") {
		lineLen := len([]rune(line))

		// A single line longer than the limit has no boundary to break on.
		if lineLen > limit {
			flush()
			for _, chunk := range chunkRunes(line, limit) {
				parts = append(parts, chunk)
			}
			continue
		}
		if currentLen+lineLen > limit {
			flush()
		}
		current.WriteString(line)
		currentLen += lineLen
	}
	flush()

	return parts
}

func chunkRunes(s string, size int) []string {
	runes := []rune(s)
	var out []string
	for start := 0; start < len(runes); start += size {
		end := min(start+size, len(runes))
		out = append(out, string(runes[start:end]))
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
