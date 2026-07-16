package httpgo

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"runtime/debug"
	"strings"

	"golang.org/x/net/http2"
)

const http2ClientPreface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
const http2PrefaceLine = "PRI * HTTP/2.0\r\n"

func (s *Server) serveHTTP2(conn net.Conn, ctx context.Context, upgrade *http.Request, settings []byte) {
	conf := &http2.Server{}
	if c := s.HTTP2Config; c != nil {
		conf.MaxConcurrentStreams = c.MaxConcurrentStreams
		conf.MaxReadFrameSize = c.MaxReadFrameSize
		conf.IdleTimeout = c.IdleTimeout
		conf.ReadIdleTimeout = c.ReadIdleTimeout
		conf.PingTimeout = c.PingTimeout
		conf.WriteByteTimeout = c.WriteByteTimeout
	}
	base := &http.Server{
		ReadTimeout:       s.ReadTimeout,
		ReadHeaderTimeout: s.ReadHeaderTimeout,
		WriteTimeout:      s.WriteTimeout,
		IdleTimeout:       s.IdleTimeout,
		MaxHeaderBytes:    s.maxHeaderBytes(),
		ErrorLog:          s.ErrorLog,
	}
	conf.ServeConn(conn, &http2.ServeConnOpts{
		Context:        ctx,
		BaseConfig:     base,
		Handler:        h2Handler{s: s},
		UpgradeRequest: upgrade,
		Settings:       settings,
	})
}

type h2Handler struct{ s *Server }

func (h h2Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.s.acquireHandler() {
		http.Error(w, "server overloaded", StatusServiceUnavailable)
		return
	}
	defer h.s.activeHandle.Add(-1)
	req := fromStdRequest(r)
	rw := &h2ResponseWriter{w: w}
	defer func() {
		if v := recover(); v != nil {
			h.s.logger().Printf("panic serving HTTP/2 %s: %v\n%s", r.RemoteAddr, v, debug.Stack())
			if !rw.wroteHeader {
				Error(rw, "internal server error", StatusInternalServerError)
			}
		}
	}()
	h.s.handler().ServeHTTP(rw, req)
}

func fromStdRequest(r *http.Request) *Request {
	req := &Request{
		Method:        r.Method,
		URL:           r.URL,
		Proto:         r.Proto,
		ProtoMajor:    r.ProtoMajor,
		ProtoMinor:    r.ProtoMinor,
		Body:          r.Body,
		ContentLength: r.ContentLength,
		Host:          r.Host,
		RemoteAddr:    r.RemoteAddr,
		RequestURI:    r.RequestURI,
		TLS:           r.TLS,
		ctx:           r.Context(),
	}
	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	for key, values := range r.Trailer {
		for _, value := range values {
			req.Trailer.Add(key, value)
		}
	}
	return req
}

type h2ResponseWriter struct {
	w           http.ResponseWriter
	header      Header
	wroteHeader bool
}

func (w *h2ResponseWriter) Header() *Header { return &w.header }

func (w *h2ResponseWriter) syncHeader() {
	dst := w.w.Header()
	w.header.Range(func(key, value string) bool {
		dst.Add(key, value)
		return true
	})
}

func (w *h2ResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.syncHeader()
	w.w.WriteHeader(code)
}

func (w *h2ResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(StatusOK)
	}
	return w.w.Write(p)
}

func (w *h2ResponseWriter) WriteString(value string) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(StatusOK)
	}
	return io.WriteString(w.w, value)
}

func (w *h2ResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(StatusOK)
	}
	if f, ok := w.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *h2ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, ErrNotSupported
}

func (w *h2ResponseWriter) Push(target string, opts *PushOptions) error {
	if p, ok := w.w.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return ErrNotSupported
}

func (s *Server) tryH2CUpgrade(cs *connState) bool {
	if !strings.EqualFold(cs.req.Header.Get("Upgrade"), "h2c") ||
		!cs.req.Header.connectionHas("Upgrade") ||
		!cs.req.Header.connectionHas("HTTP2-Settings") {
		return false
	}
	encoded := strings.TrimSpace(cs.req.Header.Get("HTTP2-Settings"))
	settings, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(settings)%6 != 0 {
		writeSimpleResponse(cs.conn, StatusBadRequest, "invalid HTTP2-Settings")
		return true
	}
	if cs.req.Body != nil {
		if _, err = io.Copy(io.Discard, cs.req.Body); err != nil {
			writeSimpleResponse(cs.conn, StatusBadRequest, "invalid h2c request body")
			return true
		}
	}
	stdReq := toStdRequest(&cs.req)
	if _, err := cs.bw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: h2c\r\n\r\n"); err != nil {
		return true
	}
	if err := cs.bw.Flush(); err != nil {
		return true
	}
	s.serveHTTP2(&bufferedConn{Conn: cs.conn, r: cs.br}, cs.ctx, stdReq, settings)
	return true
}

func toStdRequest(r *Request) *http.Request {
	h := make(http.Header)
	r.Header.Range(func(key, value string) bool {
		h.Add(key, value)
		return true
	})
	trailer := make(http.Header)
	r.Trailer.Range(func(key, value string) bool {
		trailer.Add(key, value)
		return true
	})
	return &http.Request{
		Method:           r.Method,
		URL:              r.URL,
		Proto:            r.Proto,
		ProtoMajor:       r.ProtoMajor,
		ProtoMinor:       r.ProtoMinor,
		Header:           h,
		Body:             http.NoBody,
		ContentLength:    0,
		TransferEncoding: nil,
		Host:             r.Host,
		RemoteAddr:       r.RemoteAddr,
		RequestURI:       r.RequestURI,
		TLS:              r.TLS,
		Trailer:          trailer,
	}
}
