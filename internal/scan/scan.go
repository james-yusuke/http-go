package scan

import "strings"

func IndexCRLF(b []byte) int {
	for off := 0; off < len(b); {
		i := IndexByte(b[off:], '\r')
		if i < 0 {
			return -1
		}
		i += off
		if i+1 < len(b) && b[i+1] == '\n' {
			return i
		}
		off = i + 1
	}
	return -1
}

func HasCtl(b []byte) bool {
	for _, c := range b {
		if c < 0x20 && c != '\t' || c == 0x7f {
			return true
		}
	}
	return false
}

func EqualFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		x, y := a[i], b[i]
		if x == y {
			continue
		}
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

func ValidToken(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if c <= 0x20 || c >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", rune(c)) {
			return false
		}
	}
	return true
}

func ParseDecimal(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' || n > (1<<63-1-int64(c-'0'))/10 {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

func ParseHex(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for i := range len(s) {
		c := s[i]
		var v byte
		switch {
		case '0' <= c && c <= '9':
			v = c - '0'
		case 'a' <= c && c <= 'f':
			v = c - 'a' + 10
		case 'A' <= c && c <= 'F':
			v = c - 'A' + 10
		default:
			return 0, false
		}
		if n > (1<<63-1-int64(v))/16 {
			return 0, false
		}
		n = n*16 + int64(v)
	}
	return n, true
}
