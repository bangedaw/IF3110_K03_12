package migration

import (
	"fmt"
	"strconv"
	"strings"
)

// scanSQL memecah teks SQL menjadi statement (dipisah ';' di luar literal,
// identifier berkutip, komentar, dan dollar-quote) dan mengumpulkan nomor
// placeholder $n di luar literal. Statement yang hanya berisi spasi/komentar
// diabaikan. Pengganti paket sqlparse yang tidak ada di repo; hanya untuk
// validasi bentuk mode batch (definition.go).
//
// Batasan: escape backslash pada E'...' tidak dikenali.
func scanSQL(src string) (stmts []string, placeholders []int, err error) {
	n := len(src)
	start, i := 0, 0
	hasToken := false

	for i < n {
		c := src[i]
		switch {
		case c == '-' && i+1 < n && src[i+1] == '-':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			depth := 1
			i += 2
			for i < n && depth > 0 {
				switch {
				case src[i] == '/' && i+1 < n && src[i+1] == '*':
					depth++
					i += 2
				case src[i] == '*' && i+1 < n && src[i+1] == '/':
					depth--
					i += 2
				default:
					i++
				}
			}
			if depth > 0 {
				return nil, nil, fmt.Errorf("komentar blok tidak ditutup")
			}
		case c == '\'' || c == '"':
			q := c
			i++
			closed := false
			for i < n {
				if src[i] == q {
					if i+1 < n && src[i+1] == q { // '' atau "" = escape
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, nil, fmt.Errorf("literal %q tidak ditutup", string(q))
			}
			hasToken = true
		case c == '$':
			hasToken = true
			j := i + 1
			for j < n && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			if j > i+1 {
				num, _ := strconv.Atoi(src[i+1 : j])
				placeholders = append(placeholders, num)
				i = j
				continue
			}
			j = i + 1
			for j < n && isTagChar(src[j]) {
				j++
			}
			if j < n && src[j] == '$' {
				tag := src[i : j+1]
				end := strings.Index(src[j+1:], tag)
				if end < 0 {
					return nil, nil, fmt.Errorf("dollar-quote %s tidak ditutup", tag)
				}
				i = j + 1 + end + len(tag)
				continue
			}
			i++
		case c == ';':
			if hasToken {
				stmts = append(stmts, strings.TrimSpace(src[start:i]))
			}
			start = i + 1
			hasToken = false
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		default:
			hasToken = true
			i++
		}
	}
	if hasToken {
		stmts = append(stmts, strings.TrimSpace(src[start:]))
	}
	return stmts, placeholders, nil
}

func isTagChar(c byte) bool {
	return c == '_' || c >= 0x80 ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
