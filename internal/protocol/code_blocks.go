package protocol

import "strings"

// CodeFence recognizes Markdown backtick/tilde fences, with up to three leading
// spaces. The suffix is a language label on an opening fence, empty on a close.
func CodeFence(line string) (mark byte, count int, suffix string) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || trimmed == "" || trimmed[0] != '`' && trimmed[0] != '~' {
		return 0, 0, ""
	}
	mark = trimmed[0]
	for count < len(trimmed) && trimmed[count] == mark {
		count++
	}
	return mark, count, trimmed[count:]
}
