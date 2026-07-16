package httpgo

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func startTestServer(t *testing.T, h Handler) (*Server, net.Listener) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Handler: h, ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second}
	go func() {
		if err := s.Serve(ln); err != nil && err != ErrServerClosed {
			t.Errorf("Serve: %v", err)
		}
	}()
	t.Cleanup(func() { _ = s.Close() })
	return s, ln
}

func TestServerKeepAliveAndBody(t *testing.T) {
	_, ln := startTestServer(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			Error(w, err.Error(), StatusBadRequest)
			return
		}
		w.Header().Set("X-Method", r.Method)
		_, _ = w.WriteString(r.URL.Path + ":" + string(body))
	}))
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprint(conn, "POST /one HTTP/1.1\r\nHost: test\r\nContent-Length: 3\r\n\r\nabcGET /two HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n")
	br := bufio.NewReader(conn)
	for i, want := range []string{"/one:abc", "/two:"} {
		resp, err := http.ReadResponse(br, &http.Request{Method: MethodGet})
		if err != nil {
			t.Fatalf("response %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != want || resp.StatusCode != StatusOK {
			t.Fatalf("response %d = %d %q", i, resp.StatusCode, body)
		}
	}
}

func TestShortHTTP10Request(t *testing.T) {
	_, ln := startTestServer(t, HandlerFunc(func(w ResponseWriter, _ *Request) { _, _ = w.WriteString("ok") }))
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprint(conn, "GET / HTTP/1.0\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
}

func TestServerChunkedAndTrailer(t *testing.T) {
	_, ln := startTestServer(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			Error(w, err.Error(), StatusBadRequest)
			return
		}
		_, _ = w.WriteString(string(body) + ":" + r.Trailer.Get("X-End"))
	}))
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\nTrailer: X-End\r\nConnection: close\r\n\r\n3\r\nabc\r\n0\r\nX-End: yes\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "abc:yes" {
		t.Fatalf("body = %q", body)
	}
}

func TestServerRejectsSmuggling(t *testing.T) {
	_, ln := startTestServer(t, HandlerFunc(func(w ResponseWriter, _ *Request) { _, _ = w.WriteString("bad") }))
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: test\r\nContent-Length: 3\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestExpect100Continue(t *testing.T) {
	_, ln := startTestServer(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(body)
	}))
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: test\r\nContent-Length: 3\r\nExpect: 100-continue\r\nConnection: close\r\n\r\n")
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil || line != "HTTP/1.1 100 Continue\r\n" {
		t.Fatalf("interim response = %q, %v", line, err)
	}
	if blank, _ := br.ReadString('\n'); blank != "\r\n" {
		t.Fatalf("interim terminator = %q", blank)
	}
	_, _ = fmt.Fprint(conn, "abc")
	resp, err := http.ReadResponse(br, &http.Request{Method: MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "abc" {
		t.Fatalf("body = %q", body)
	}
}

func TestServerRejectsDuplicateHost(t *testing.T) {
	_, ln := startTestServer(t, HandlerFunc(func(w ResponseWriter, _ *Request) { _, _ = w.WriteString("bad") }))
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: one\r\nHost: two\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHTTP2Adapter(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	s := &Server{Handler: HandlerFunc(func(w ResponseWriter, r *Request) {
		w.Header().Set("X-Proto", r.Proto)
		_, _ = w.WriteString("h2")
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.serveHTTP2(serverConn, ctx, nil, nil)
	cc, err := (&http2.Transport{}).NewClientConn(clientConn)
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	req, _ := http.NewRequest(MethodGet, "https://example.test/", nil)
	resp, err := cc.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "h2" || !strings.HasPrefix(resp.Header.Get("X-Proto"), "HTTP/2") {
		t.Fatalf("response = %q proto=%q", body, resp.Header.Get("X-Proto"))
	}
}

func TestH2CPriorKnowledge(t *testing.T) {
	_, ln := startTestServer(t, HandlerFunc(func(w ResponseWriter, r *Request) {
		_, _ = w.WriteString(r.Proto)
	}))
	tr := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, _ string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, ln.Addr().String())
		},
	}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequest(MethodGet, "http://"+ln.Addr().String()+"/", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "HTTP/2.0" {
		t.Fatalf("body = %q", body)
	}
}

func TestTLSALPNHTTP2(t *testing.T) {
	donor := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := donor.TLS.Certificates[0]
	donor.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
		Handler: HandlerFunc(func(w ResponseWriter, r *Request) {
			_, _ = w.WriteString(r.Proto)
		}),
	}
	go func() { _ = s.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = s.Close() })

	tr := &http2.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	resp, err := client.Get("https://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "HTTP/2.0" || resp.TLS.NegotiatedProtocol != "h2" {
		t.Fatalf("body=%q alpn=%q", body, resp.TLS.NegotiatedProtocol)
	}
}

func TestShutdownWakesIdleConnection(t *testing.T) {
	s, ln := startTestServer(t, HandlerFunc(func(w ResponseWriter, _ *Request) { _, _ = w.WriteString("ok") }))
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func FuzzRequestTarget(f *testing.F) {
	for _, seed := range []string{"/", "/a?b=c", "/a%20b", "*", "http://example.com/x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, target string) {
		var u url.URL
		_ = parseTarget(&u, MethodGet, target)
	})
}
