package httpgo

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/url"
)

type pathValue struct {
	name  string
	value string
}

// Request describes an incoming request. Unless Clone is called, string and
// header data must not be retained after the handler returns.
type Request struct {
	Method        string
	URL           *url.URL
	Proto         string
	ProtoMajor    int
	ProtoMinor    int
	Header        Header
	Body          io.ReadCloser
	ContentLength int64
	Trailer       Header
	Host          string
	RemoteAddr    string
	RequestURI    string
	TLS           *tls.ConnectionState

	ctx        context.Context
	pathValues []pathValue
	life       *lifetime
}

func (r *Request) Context() context.Context {
	if r == nil || r.ctx == nil {
		return context.Background()
	}
	return r.ctx
}

func (r *Request) WithContext(ctx context.Context) *Request {
	if ctx == nil {
		panic("httpgo: nil Context")
	}
	r2 := new(Request)
	*r2 = *r
	r2.ctx = ctx
	return r2
}

// Clone returns a deep copy of request metadata. As in net/http, Body is
// shallow-copied because a streaming body cannot be duplicated safely.
func (r *Request) Clone(ctx context.Context) *Request {
	if ctx == nil {
		panic("httpgo: nil Context")
	}
	r2 := new(Request)
	*r2 = *r
	r2.ctx = ctx
	r2.life = nil
	r2.Header = r.Header.Clone()
	r2.Trailer = r.Trailer.Clone()
	if r.URL != nil {
		u := *r.URL
		r2.URL = &u
	}
	if r.TLS != nil {
		t := *r.TLS
		r2.TLS = &t
	}
	r2.pathValues = append([]pathValue(nil), r.pathValues...)
	return r2
}

func (r *Request) PathValue(name string) string {
	if r == nil {
		return ""
	}
	for i := range r.pathValues {
		if r.pathValues[i].name == name {
			return r.pathValues[i].value
		}
	}
	return ""
}

func (r *Request) SetPathValue(name, value string) {
	for i := range r.pathValues {
		if r.pathValues[i].name == name {
			r.pathValues[i].value = value
			return
		}
	}
	r.pathValues = append(r.pathValues, pathValue{name: name, value: value})
}

func (r *Request) Cookies() []*Cookie {
	h := make(http.Header)
	for _, value := range r.Header.Values("Cookie") {
		h.Add("Cookie", value)
	}
	return (&http.Request{Header: h}).Cookies()
}

func (r *Request) Cookie(name string) (*Cookie, error) {
	for _, c := range r.Cookies() {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, http.ErrNoCookie
}

func (r *Request) AddCookie(c *Cookie) {
	if c == nil {
		return
	}
	t := &http.Request{Header: make(http.Header)}
	t.AddCookie(c)
	r.Header.Add("Cookie", t.Header.Get("Cookie"))
}
