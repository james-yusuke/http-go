package httpgo

import (
	"net/textproto"
	"strings"
)

type headerEntry struct {
	key   string
	value string
}

// Header is a compact, insertion-ordered HTTP header collection. Values read
// from an incoming request are borrowed until ServeHTTP returns. Clone creates
// an owned copy.
type Header struct {
	entries []headerEntry
	life    *lifetime
	gen     uint64
}

func (h *Header) check() {
	if h != nil {
		checkLifetime(h.life, h.gen)
	}
}

func (h *Header) reset(l *lifetime) {
	h.entries = h.entries[:0]
	h.life = l
	if l != nil {
		h.gen = l.gen
	}
}

func (h *Header) Get(key string) string {
	h.check()
	if h == nil {
		return ""
	}
	for i := range h.entries {
		if strings.EqualFold(h.entries[i].key, key) {
			return h.entries[i].value
		}
	}
	return ""
}

func (h *Header) Values(key string) []string {
	h.check()
	if h == nil {
		return nil
	}
	var out []string
	for i := range h.entries {
		if strings.EqualFold(h.entries[i].key, key) {
			out = append(out, h.entries[i].value)
		}
	}
	return out
}

func (h *Header) Has(key string) bool {
	h.check()
	if h == nil {
		return false
	}
	for i := range h.entries {
		if strings.EqualFold(h.entries[i].key, key) {
			return true
		}
	}
	return false
}

func (h *Header) firstAndCount(key string) (string, int) {
	h.check()
	var first string
	count := 0
	for i := range h.entries {
		if strings.EqualFold(h.entries[i].key, key) {
			if count == 0 {
				first = h.entries[i].value
			}
			count++
		}
	}
	return first, count
}

func (h *Header) Set(key, value string) {
	h.check()
	key = textproto.CanonicalMIMEHeaderKey(key)
	first := -1
	for i := 0; i < len(h.entries); {
		if strings.EqualFold(h.entries[i].key, key) {
			if first < 0 {
				first = i
				h.entries[i] = headerEntry{key: key, value: value}
				i++
				continue
			}
			copy(h.entries[i:], h.entries[i+1:])
			h.entries = h.entries[:len(h.entries)-1]
			continue
		}
		i++
	}
	if first < 0 {
		h.entries = append(h.entries, headerEntry{key: key, value: value})
	}
}

func (h *Header) Add(key, value string) {
	h.check()
	h.entries = append(h.entries, headerEntry{key: textproto.CanonicalMIMEHeaderKey(key), value: value})
}

func (h *Header) Del(key string) {
	h.check()
	for i := 0; i < len(h.entries); {
		if strings.EqualFold(h.entries[i].key, key) {
			copy(h.entries[i:], h.entries[i+1:])
			h.entries = h.entries[:len(h.entries)-1]
			continue
		}
		i++
	}
}

// Range visits every header field line in insertion order. Returning false
// stops iteration. The strings follow the Header's borrowing rules.
func (h *Header) Range(fn func(key, value string) bool) {
	h.check()
	if h == nil {
		return
	}
	for i := range h.entries {
		if !fn(h.entries[i].key, h.entries[i].value) {
			return
		}
	}
}

func (h *Header) Clone() Header {
	h.check()
	if h == nil {
		return Header{}
	}
	c := Header{entries: make([]headerEntry, len(h.entries))}
	copy(c.entries, h.entries)
	return c
}

func (h *Header) copyFrom(src *Header) {
	h.entries = append(h.entries[:0], src.entries...)
	h.life = nil
	h.gen = 0
}

func (h *Header) connectionHas(token string) bool {
	for _, value := range h.Values("Connection") {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
