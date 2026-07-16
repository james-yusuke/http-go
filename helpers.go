package httpgo

import (
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func Error(w ResponseWriter, error string, code int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = w.WriteString(error + "\n")
}

func NotFound(w ResponseWriter, _ *Request) { Error(w, "404 page not found", StatusNotFound) }

func Redirect(w ResponseWriter, r *Request, target string, code int) {
	if u, err := url.Parse(target); err == nil && u.Scheme == "" && u.Host == "" && !strings.HasPrefix(target, "/") && r != nil && r.URL != nil {
		base := r.URL.Path
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			target = base[:i+1] + target
		}
	}
	w.Header().Set("Location", target)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.WriteString("<a href=\"" + html.EscapeString(target) + "\">" + http.StatusText(code) + "</a>.\n")
}

func SetCookie(w ResponseWriter, cookie *Cookie) {
	if cookie != nil {
		w.Header().Add("Set-Cookie", cookie.String())
	}
}

type dateValue struct {
	second int64
	value  string
}

var cachedHTTPDate atomic.Pointer[dateValue]

func appendHTTPDate() string {
	now := time.Now()
	second := now.Unix()
	if cached := cachedHTTPDate.Load(); cached != nil && cached.second == second {
		return cached.value
	}
	next := &dateValue{second: second, value: now.UTC().Format(http.TimeFormat)}
	cachedHTTPDate.Store(next)
	return next.value
}

func writeSimpleResponse(conn netWriter, status int, message string) {
	body := message + "\n"
	_, _ = conn.Write([]byte("HTTP/1.1 " + strconv.Itoa(status) + " " + StatusText(status) + "\r\nContent-Type: text/plain; charset=utf-8\r\nConnection: close\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body))
}

type netWriter interface{ Write([]byte) (int, error) }
