package httpgo

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

type routeSegment struct {
	literal string
	name    string
	wild    bool
	multi   bool
	end     bool
}

type route struct {
	pattern string
	method  string
	host    string
	segs    []routeSegment
	h       Handler
	score   int
}

// ServeMux routes requests using the modern net/http pattern shape:
// "METHOD host/path/{name}/{rest...}" and "{$}".
type ServeMux struct {
	mu        sync.RWMutex
	routes    []route
	validator *http.ServeMux
}

func NewServeMux() *ServeMux { return new(ServeMux) }

func (m *ServeMux) Handle(pattern string, handler Handler) {
	if handler == nil {
		panic("httpgo: nil handler")
	}
	r, err := parsePattern(pattern)
	if err != nil {
		panic(err)
	}
	r.h = handler
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.validator == nil {
		m.validator = http.NewServeMux()
	}
	// Reuse the standard library's complete modern-pattern conflict checker.
	// Matching remains in httpgo's allocation-free route representation.
	m.validator.Handle(pattern, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	m.routes = append(m.routes, r)
}

func (m *ServeMux) HandleFunc(pattern string, handler func(ResponseWriter, *Request)) {
	m.Handle(pattern, HandlerFunc(handler))
}

func (m *ServeMux) Handler(r *Request) (Handler, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	best := -1
	bestScore := -1
	for i := range m.routes {
		if routeMatches(&m.routes[i], r, false, false) && m.routes[i].score > bestScore {
			best, bestScore = i, m.routes[i].score
		}
	}
	if best < 0 {
		return HandlerFunc(NotFound), ""
	}
	return m.routes[best].h, m.routes[best].pattern
}

func (m *ServeMux) ServeHTTP(w ResponseWriter, req *Request) {
	m.mu.RLock()
	best := -1
	bestScore := -1
	for i := range m.routes {
		if routeMatches(&m.routes[i], req, false, false) && m.routes[i].score > bestScore {
			best, bestScore = i, m.routes[i].score
		}
	}
	if best < 0 {
		methodMismatch := false
		for i := range m.routes {
			if routeMatches(&m.routes[i], req, true, false) {
				methodMismatch = true
				break
			}
		}
		m.mu.RUnlock()
		if methodMismatch {
			Error(w, StatusText(StatusMethodNotAllowed), StatusMethodNotAllowed)
		} else {
			NotFound(w, req)
		}
		return
	}
	r := m.routes[best]
	m.mu.RUnlock()
	req.pathValues = req.pathValues[:0]
	routeMatches(&r, req, false, true)
	r.h.ServeHTTP(w, req)
}

func parsePattern(pattern string) (route, error) {
	original := pattern
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return route{}, fmt.Errorf("httpgo: empty pattern")
	}
	var method string
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		method, pattern = pattern[:i], strings.TrimSpace(pattern[i+1:])
		if method == "" || strings.ContainsAny(method, "\t/") {
			return route{}, fmt.Errorf("httpgo: invalid method in pattern %q", original)
		}
	}
	host := ""
	if !strings.HasPrefix(pattern, "/") {
		i := strings.IndexByte(pattern, '/')
		if i < 0 {
			return route{}, fmt.Errorf("httpgo: pattern %q has no path", original)
		}
		host, pattern = pattern[:i], pattern[i:]
	}
	parts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	segs := make([]routeSegment, 0, len(parts))
	score := 0
	seen := make(map[string]bool)
	for i, p := range parts {
		s := routeSegment{}
		switch {
		case p == "" && i == len(parts)-1 && strings.HasSuffix(pattern, "/"):
			s.wild = true
			s.multi = true
			score++
		case p == "{$}":
			if i != len(parts)-1 {
				return route{}, fmt.Errorf("httpgo: {$} must end pattern %q", original)
			}
			s.end = true
			score += 8
		case strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}"):
			name := p[1 : len(p)-1]
			if strings.HasSuffix(name, "...") {
				name = strings.TrimSuffix(name, "...")
				s.multi = true
				if i != len(parts)-1 {
					return route{}, fmt.Errorf("httpgo: multi wildcard must end pattern %q", original)
				}
			}
			if name == "" || seen[name] {
				return route{}, fmt.Errorf("httpgo: invalid wildcard in pattern %q", original)
			}
			seen[name] = true
			s.name = name
			s.wild = true
			score += 2
		default:
			if strings.ContainsAny(p, "{}") {
				return route{}, fmt.Errorf("httpgo: invalid segment in pattern %q", original)
			}
			s.literal = p
			score += 16
		}
		segs = append(segs, s)
	}
	if method != "" {
		score += 4
	}
	if host != "" {
		score += 32
	}
	return route{pattern: original, method: method, host: host, segs: segs, score: score}, nil
}

func routeMatches(rt *route, req *Request, ignoreMethod, capture bool) bool {
	if !ignoreMethod && rt.method != "" && rt.method != req.Method && !(req.Method == MethodHead && rt.method == MethodGet) {
		return false
	}
	if rt.host != "" && !strings.EqualFold(rt.host, stripHostPort(req.Host)) {
		return false
	}
	path := "/"
	if req.URL != nil && req.URL.Path != "" {
		path = req.URL.Path
	}
	pos := 1
	for i, seg := range rt.segs {
		if seg.end {
			return pos >= len(path)
		}
		if seg.multi {
			value := ""
			if pos < len(path) {
				value = path[pos:]
			}
			if capture && seg.name != "" {
				req.SetPathValue(seg.name, value)
			}
			return true
		}
		if pos > len(path) {
			return false
		}
		end := strings.IndexByte(path[pos:], '/')
		if end < 0 {
			end = len(path)
		} else {
			end += pos
		}
		value := path[pos:end]
		if !seg.wild && seg.literal != value {
			return false
		}
		if seg.name != "" && capture {
			req.SetPathValue(seg.name, value)
		}
		pos = end + 1
		if end == len(path) && i != len(rt.segs)-1 {
			return false
		}
	}
	return pos > len(path)
}

func stripHostPort(host string) string {
	if i := strings.LastIndexByte(host, ':'); i > 0 && !strings.Contains(host[i+1:], "]") {
		return host[:i]
	}
	return host
}
