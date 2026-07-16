package httpgo

import (
	"net/url"
	"strconv"
	"testing"
)

type recorder struct {
	header Header
	status int
	body   []byte
}

func (r *recorder) Header() *Header      { return &r.header }
func (r *recorder) WriteHeader(code int) { r.status = code }
func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = StatusOK
	}
	r.body = append(r.body, p...)
	return len(p), nil
}
func (r *recorder) WriteString(s string) (int, error) {
	return r.Write([]byte(s))
}

func TestServeMuxPatterns(t *testing.T) {
	mux := NewServeMux()
	mux.HandleFunc("GET /hello/{name}", func(w ResponseWriter, r *Request) {
		_, _ = w.WriteString(r.PathValue("name"))
	})
	mux.HandleFunc("POST example.com/files/{rest...}", func(w ResponseWriter, r *Request) {
		_, _ = w.WriteString(r.PathValue("rest"))
	})
	mux.HandleFunc("/", func(w ResponseWriter, _ *Request) { _, _ = w.WriteString("root") })

	tests := []struct {
		method, host, path, want string
	}{
		{MethodGet, "localhost", "/hello/gopher", "gopher"},
		{MethodHead, "localhost", "/hello/head", "head"},
		{MethodPost, "example.com:8080", "/files/a/b", "a/b"},
		{MethodGet, "localhost", "/", "root"},
		{MethodGet, "localhost", "/unmatched/path", "root"},
	}
	for i, tc := range tests {
		r := &Request{Method: tc.method, Host: tc.host, URL: &url.URL{Path: tc.path}}
		w := new(recorder)
		mux.ServeHTTP(w, r)
		if string(w.body) != tc.want {
			t.Errorf("case %s: got %q want %q", strconv.Itoa(i), w.body, tc.want)
		}
	}
}

func TestServeMuxConflict(t *testing.T) {
	mux := NewServeMux()
	mux.HandleFunc("GET /x/{id}", func(ResponseWriter, *Request) {})
	defer func() {
		if recover() == nil {
			t.Fatal("expected conflict panic")
		}
	}()
	mux.HandleFunc("GET /x/{name}", func(ResponseWriter, *Request) {})
}

func FuzzParsePattern(f *testing.F) {
	for _, seed := range []string{"/", "GET /x/{id}", "example.com/{rest...}", "GET /{$}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, pattern string) {
		_, _ = parsePattern(pattern)
	})
}
