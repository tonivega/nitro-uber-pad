package ui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/runenames"
)

// lineAt returns line row (0-based) of text.
func lineAt(text string, row int) string {
	for i := 0; i < row; i++ {
		j := strings.IndexByte(text, '\n')
		if j < 0 {
			return ""
		}
		text = text[j+1:]
	}
	if j := strings.IndexByte(text, '\n'); j >= 0 {
		return text[:j]
	}
	return text
}

func isExtender(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) ||
		(r >= 0xFE00 && r <= 0xFE0F) || (r >= 0x1F3FB && r <= 0x1F3FF) ||
		(r >= 0xE0020 && r <= 0xE007F) || r == 0x20E3
}

func isRegional(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// clusterEnd returns the end of the user-perceived character starting at j
// (emoji ZWJ sequences, skin tones, variation selectors, flags, combining marks).
func clusterEnd(rs []rune, j int) int {
	k := j + 1
	if isRegional(rs[j]) && k < len(rs) && isRegional(rs[k]) {
		return k + 1
	}
	for k < len(rs) {
		switch {
		case isExtender(rs[k]):
			k++
		case rs[k] == 0x200D && k+1 < len(rs):
			k += 2
		default:
			return k
		}
	}
	return k
}

func runeInfo(r rune) string {
	name := runenames.Name(r)
	if name == "" {
		name = "UNASSIGNED"
	}
	esc := fmt.Sprintf("\\u%04X", r)
	if r > 0xFFFF {
		r1, r2 := utf16.EncodeRune(r)
		esc = fmt.Sprintf("\\U%08X \\u%04X\\u%04X", r, r1, r2)
	}
	return fmt.Sprintf("U+%04X %s [UTF-8 % X] %s", r, name, []byte(string(r)), esc)
}

// needsMarker reports invisible or special characters that get a visible marker.
func needsMarker(r rune) bool {
	switch {
	case r == 0x200D || (r >= 0xFE00 && r <= 0xFE0F) || (r >= 0xE0020 && r <= 0xE007F) || r == '\t':
		return false
	case r == utf8.RuneError:
		return true
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || (unicode.Is(unicode.Zs, r) && r != ' ') ||
		unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
}

// visualize renders invisible characters as markers and truncates to limit runes.
func visualize(s string, limit int) string {
	var sb strings.Builder
	n := 0
	for _, r := range s {
		if n >= limit {
			sb.WriteString("…")
			break
		}
		if needsMarker(r) {
			fmt.Fprintf(&sb, "⟨U+%04X⟩", r)
		} else {
			sb.WriteRune(r)
		}
		n++
	}
	return sb.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexAt(s string, i, n int) (uint64, bool) {
	if i+n > len(s) {
		return 0, false
	}
	for k := i; k < i+n; k++ {
		if !isHex(s[k]) {
			return 0, false
		}
	}
	v, err := strconv.ParseUint(s[i:i+n], 16, 32)
	return v, err == nil
}

// decodeEscapes decodes \uXXXX (incl. surrogate pairs), \u{...}, \UXXXXXXXX,
// \xNN (UTF-8 byte sequences), &#NNN; / &#xHH; and U+XXXX notations.
func decodeEscapes(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'u':
				if i+2 < len(s) && s[i+2] == '{' {
					if j := strings.IndexByte(s[i+3:], '}'); j > 0 && j <= 6 {
						if v, ok := hexAt(s, i+3, j); ok {
							out = utf8.AppendRune(out, rune(v))
							i += 4 + j
							continue
						}
					}
				}
				if v, ok := hexAt(s, i+2, 4); ok {
					r := rune(v)
					if utf16.IsSurrogate(r) && i+12 <= len(s) && s[i+6] == '\\' && s[i+7] == 'u' {
						if v2, ok := hexAt(s, i+8, 4); ok {
							if d := utf16.DecodeRune(r, rune(v2)); d != unicode.ReplacementChar {
								out = utf8.AppendRune(out, d)
								i += 12
								continue
							}
						}
					}
					out = utf8.AppendRune(out, r)
					i += 6
					continue
				}
			case 'U':
				if v, ok := hexAt(s, i+2, 8); ok {
					out = utf8.AppendRune(out, rune(v))
					i += 10
					continue
				}
			case 'x':
				if v, ok := hexAt(s, i+2, 2); ok {
					out = append(out, byte(v))
					i += 4
					continue
				}
			}
		}
		if s[i] == '&' && i+3 < len(s) && s[i+1] == '#' {
			if j := strings.IndexByte(s[i:], ';'); j > 2 && j < 12 {
				body, base := s[i+2:i+j], 10
				if body[0] == 'x' || body[0] == 'X' {
					body, base = body[1:], 16
				}
				if v, err := strconv.ParseUint(body, base, 32); err == nil {
					out = utf8.AppendRune(out, rune(v))
					i += j + 1
					continue
				}
			}
		}
		if s[i] == 'U' && i+1 < len(s) && s[i+1] == '+' {
			j := i + 2
			for j < len(s) && j-i-2 < 6 && isHex(s[j]) {
				j++
			}
			if j-i-2 >= 4 {
				v, _ := strconv.ParseUint(s[i+2:j], 16, 32)
				out = utf8.AppendRune(out, rune(v))
				i = j
				continue
			}
		}
		out = append(out, s[i])
		i++
	}
	return strings.ToValidUTF8(string(out), "\uFFFD")
}

// inspectText describes the character (grapheme cluster) at the cursor column,
// with code points, names, UTF-8 bytes and escapes, plus a visual rendering of
// the line (invisible characters as markers, escapes decoded).
func inspectText(line string, col int) string {
	rs := []rune(line)
	if col < 0 {
		col = 0
	}
	var sb strings.Builder
	if len(rs) == 0 {
		sb.WriteString("Empty line")
	} else if col >= len(rs) {
		fmt.Fprintf(&sb, "End of line (%d chars) | Line: %s", len(rs), visualize(line, 80))
	} else {
		j := 0
		for j < len(rs) {
			e := clusterEnd(rs, j)
			if col < e {
				break
			}
			j = e
		}
		e := clusterEnd(rs, j)
		parts := make([]string, 0, e-j)
		for _, r := range rs[j:e] {
			parts = append(parts, runeInfo(r))
		}
		fmt.Fprintf(&sb, "Char %s | %s | Line: %s", visualize(string(rs[j:e]), 16), strings.Join(parts, " + "), visualize(line, 80))
	}
	if d := decodeEscapes(line); d != line {
		sb.WriteString(" | Decoded: ")
		sb.WriteString(visualize(d, 80))
	}
	return sb.String()
}

// DecodeEscapes replaces unicode/UTF escapes in the current view with the characters.
func (ed *Editor) DecodeEscapes() {
	if ed.mode == modeHex {
		ed.app.Alert("Not available in the hex view")
		return
	}
	t := ed.entry.Text
	d := decodeEscapes(t)
	if d == t {
		ed.app.Notify("No escapes found")
		return
	}
	row := ed.entry.CursorRow
	ed.entry.SetText(d)
	ed.entry.CursorRow = min(row, strings.Count(d, "\n"))
	ed.entry.CursorColumn = 0
	ed.entry.Refresh()
	ed.markDirty()
}
