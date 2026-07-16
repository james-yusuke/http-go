package httpgo

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxHeaderBytes = 1 << 20
	defaultBodyBufferSize = 64 << 10
	defaultMaxConcurrency = 256 * 1024
	defaultIOBufferSize   = 16 << 10
)

type HTTP2Config struct {
	MaxConcurrentStreams uint32
	MaxReadFrameSize     uint32
	IdleTimeout          time.Duration
	ReadIdleTimeout      time.Duration
	PingTimeout          time.Duration
	WriteByteTimeout     time.Duration
}

type Server struct {
	Addr                string
	Handler             Handler
	TLSConfig           *tls.Config
	ReadTimeout         time.Duration
	ReadHeaderTimeout   time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	MaxHeaderBytes      int
	BodyBufferSize      int
	MaxRequestBodyBytes int64
	MaxConnections      int
	MaxConcurrency      int
	HTTP2Config         *HTTP2Config
	ErrorLog            *log.Logger

	mu           sync.Mutex
	listeners    map[net.Listener]struct{}
	conns        map[net.Conn]struct{}
	onShutdown   []func()
	wg           sync.WaitGroup
	inShutdown   atomic.Bool
	activeConns  atomic.Int64
	activeHandle atomic.Int64
}

type connState struct {
	server          *Server
	conn            net.Conn
	br              *bufio.Reader
	bw              *bufio.Writer
	ctx             context.Context
	cancel          context.CancelFunc
	tlsState        *tls.ConnectionState
	remoteAddr      string
	reqLife         lifetime
	req             Request
	url             urlValue
	res             response
	body            requestBody
	headerBuf       []byte
	bodyBuf         []byte
	closeAfterReply bool
}

// urlValue aliases url.URL without adding an allocation to connState setup.
type urlValue = url.URL

var connPool = sync.Pool{New: func() any {
	return &connState{
		br:        bufio.NewReaderSize(nil, defaultIOBufferSize),
		bw:        bufio.NewWriterSize(nil, defaultIOBufferSize),
		headerBuf: make([]byte, 0, 4096),
		bodyBuf:   make([]byte, 0, defaultBodyBufferSize),
	}
}}

var DefaultServeMux = NewServeMux()

func Handle(pattern string, handler Handler) { DefaultServeMux.Handle(pattern, handler) }
func HandleFunc(pattern string, handler func(ResponseWriter, *Request)) {
	DefaultServeMux.HandleFunc(pattern, handler)
}

func ListenAndServe(addr string, handler Handler) error {
	s := &Server{Addr: addr, Handler: handler}
	return s.ListenAndServe()
}

func ListenAndServeTLS(addr, certFile, keyFile string, handler Handler) error {
	s := &Server{Addr: addr, Handler: handler}
	return s.ListenAndServeTLS(certFile, keyFile)
}

func (s *Server) handler() Handler {
	if s.Handler != nil {
		return s.Handler
	}
	return DefaultServeMux
}

func (s *Server) maxHeaderBytes() int {
	if s.MaxHeaderBytes > 0 {
		return s.MaxHeaderBytes
	}
	return defaultMaxHeaderBytes
}

func (s *Server) bodyBufferSize() int {
	if s.BodyBufferSize > 0 {
		return s.BodyBufferSize
	}
	return defaultBodyBufferSize
}

func (s *Server) responseBufferSize() int { return s.bodyBufferSize() }

func (s *Server) maxConnections() int {
	if s.MaxConnections > 0 {
		return s.MaxConnections
	}
	return defaultMaxConcurrency
}

func (s *Server) maxConcurrency() int {
	if s.MaxConcurrency > 0 {
		return s.MaxConcurrency
	}
	return defaultMaxConcurrency
}

func (s *Server) logger() *log.Logger {
	if s.ErrorLog != nil {
		return s.ErrorLog
	}
	return log.New(os.Stderr, "httpgo: ", log.LstdFlags)
}

func (s *Server) ListenAndServe() error {
	if s.inShutdown.Load() {
		return ErrServerClosed
	}
	addr := s.Addr
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

func (s *Server) ListenAndServeTLS(certFile, keyFile string) error {
	if s.inShutdown.Load() {
		return ErrServerClosed
	}
	addr := s.Addr
	if addr == "" {
		addr = ":https"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.ServeTLS(ln, certFile, keyFile)
}

func (s *Server) ServeTLS(ln net.Listener, certFile, keyFile string) error {
	config := new(tls.Config)
	if s.TLSConfig != nil {
		config = s.TLSConfig.Clone()
	}
	if len(config.Certificates) == 0 && config.GetCertificate == nil {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			_ = ln.Close()
			return err
		}
		config.Certificates = []tls.Certificate{cert}
	}
	config.NextProtos = ensureNextProtos(config.NextProtos)
	return s.Serve(tls.NewListener(ln, config))
}

func ensureNextProtos(in []string) []string {
	hasH2, hasH1 := false, false
	for _, p := range in {
		hasH2 = hasH2 || p == "h2"
		hasH1 = hasH1 || p == "http/1.1"
	}
	out := append([]string(nil), in...)
	if !hasH2 {
		out = append(out, "h2")
	}
	if !hasH1 {
		out = append(out, "http/1.1")
	}
	return out
}

func (s *Server) Serve(ln net.Listener) error {
	if s.inShutdown.Load() {
		_ = ln.Close()
		return ErrServerClosed
	}
	s.mu.Lock()
	if s.inShutdown.Load() {
		s.mu.Unlock()
		_ = ln.Close()
		return ErrServerClosed
	}
	if s.listeners == nil {
		s.listeners = make(map[net.Listener]struct{})
		s.conns = make(map[net.Conn]struct{})
	}
	s.listeners[ln] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.listeners, ln)
		s.mu.Unlock()
	}()

	var delay time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.inShutdown.Load() || errors.Is(err, net.ErrClosed) {
				return ErrServerClosed
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if delay == 0 {
					delay = 5 * time.Millisecond
				} else {
					delay *= 2
				}
				if delay > time.Second {
					delay = time.Second
				}
				time.Sleep(delay)
				continue
			}
			return err
		}
		delay = 0
		if s.activeConns.Add(1) > int64(s.maxConnections()) {
			s.activeConns.Add(-1)
			writeSimpleResponse(conn, StatusServiceUnavailable, "server overloaded")
			_ = conn.Close()
			continue
		}
		if !s.startConn(conn) {
			s.activeConns.Add(-1)
			_ = conn.Close()
			continue
		}
		go func() {
			defer s.wg.Done()
			defer s.activeConns.Add(-1)
			defer s.trackConn(conn, false)
			s.serveConn(conn)
		}()
	}
}

func (s *Server) startConn(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inShutdown.Load() {
		return false
	}
	s.conns[c] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) trackConn(c net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[c] = struct{}{}
	} else {
		delete(s.conns, c)
	}
}

func (s *Server) serveConn(conn net.Conn) {
	cs := connPool.Get().(*connState)
	cs.server = s
	cs.conn = conn
	cs.br.Reset(conn)
	cs.bw.Reset(conn)
	cs.closeAfterReply = false
	cs.res.hijacked = false
	cs.tlsState = nil
	cs.remoteAddr = conn.RemoteAddr().String()
	cs.ctx, cs.cancel = context.WithCancel(context.Background())
	defer func() {
		cs.cancel()
		if cs.res.hijacked {
			// Ownership of the connection and buffered reader/writer moved to
			// the caller. They must not be reset or returned to the pool.
			return
		}
		_ = conn.Close()
		cs.server, cs.conn, cs.ctx, cs.cancel, cs.tlsState, cs.remoteAddr = nil, nil, nil, nil, nil, ""
		cs.br.Reset(nil)
		cs.bw.Reset(nil)
		if cap(cs.headerBuf) > 64<<10 {
			cs.headerBuf = make([]byte, 0, 4096)
		}
		connPool.Put(cs)
	}()

	if tc, ok := conn.(*tls.Conn); ok {
		if d := s.ReadHeaderTimeout; d > 0 {
			_ = conn.SetDeadline(time.Now().Add(d))
		}
		if err := tc.HandshakeContext(cs.ctx); err != nil {
			return
		}
		state := tc.ConnectionState()
		cs.tlsState = &state
		if state.NegotiatedProtocol == "h2" {
			_ = conn.SetDeadline(time.Time{})
			s.serveHTTP2(conn, cs.ctx, nil, nil)
			return
		}
	}

	if d := s.ReadHeaderTimeout; d > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(d))
	}
	if line, err := cs.br.Peek(len(http2PrefaceLine)); err == nil && string(line) == http2PrefaceLine {
		if preface, err := cs.br.Peek(len(http2ClientPreface)); err == nil && string(preface) == http2ClientPreface {
			_ = conn.SetReadDeadline(time.Time{})
			s.serveHTTP2(&bufferedConn{Conn: conn, r: cs.br}, cs.ctx, nil, nil)
			return
		}
	}

	firstRequest := true
	for !s.inShutdown.Load() {
		cs.reqLife.activate()
		cs.closeAfterReply = false
		if !firstRequest && s.IdleTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(s.IdleTimeout))
		} else if d := s.ReadHeaderTimeout; d > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(d))
		} else if d := s.ReadTimeout; d > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(d))
		} else {
			_ = conn.SetReadDeadline(time.Time{})
		}
		err := cs.readRequest()
		firstRequest = false
		if err != nil {
			cs.reqLife.release()
			if !errors.Is(err, io.EOF) {
				status := StatusBadRequest
				if pe := new(parseError); errors.As(err, &pe) {
					status = pe.status
				}
				writeSimpleResponse(conn, status, StatusText(status))
			}
			return
		}
		if s.tryH2CUpgrade(cs) {
			cs.reqLife.release()
			return
		}
		if d := s.WriteTimeout; d > 0 {
			_ = conn.SetWriteDeadline(time.Now().Add(d))
		}
		cs.res.reset(cs)
		if !s.acquireHandler() {
			cs.closeAfterReply = true
			Error(&cs.res, "server overloaded", StatusServiceUnavailable)
		} else {
			s.invokeHandler(&cs.res, &cs.req)
			s.activeHandle.Add(-1)
		}
		bodyErr := cs.req.Body.Close()
		if bodyErr != nil {
			cs.closeAfterReply = true
		}
		if cs.req.Header.connectionHas("close") || cs.req.ProtoMinor == 0 && !cs.req.Header.connectionHas("keep-alive") {
			cs.closeAfterReply = true
		}
		if s.inShutdown.Load() {
			cs.closeAfterReply = true
		}
		flush := cs.closeAfterReply || cs.br.Buffered() == 0
		if err := cs.res.finish(flush); err != nil {
			cs.reqLife.release()
			return
		}
		hijacked := cs.res.hijacked
		cs.reqLife.release()
		if hijacked || cs.closeAfterReply {
			return
		}
	}
}

func (s *Server) acquireHandler() bool {
	max := int64(s.maxConcurrency())
	for {
		n := s.activeHandle.Load()
		if n >= max {
			return false
		}
		if s.activeHandle.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func (s *Server) invokeHandler(w ResponseWriter, r *Request) {
	defer func() {
		if v := recover(); v != nil {
			s.logger().Printf("panic serving %s: %v\n%s", r.RemoteAddr, v, debug.Stack())
			if rw, ok := w.(*response); ok && !rw.wroteHeader {
				Error(w, "internal server error", StatusInternalServerError)
				rw.cs.closeAfterReply = true
			}
		}
	}()
	s.handler().ServeHTTP(w, r)
}

func (s *Server) RegisterOnShutdown(f func()) {
	if f == nil {
		return
	}
	s.mu.Lock()
	s.onShutdown = append(s.onShutdown, f)
	s.mu.Unlock()
}

func (s *Server) Close() error {
	s.inShutdown.Store(true)
	s.mu.Lock()
	var first error
	for ln := range s.listeners {
		if err := ln.Close(); err != nil && first == nil {
			first = err
		}
	}
	for c := range s.conns {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.mu.Unlock()
	return first
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.inShutdown.Store(true)
	s.mu.Lock()
	for ln := range s.listeners {
		_ = ln.Close()
	}
	// Wake keep-alive connections blocked waiting for another request. An
	// active handler can still finish and write because only the read deadline
	// is changed.
	for c := range s.conns {
		_ = c.SetReadDeadline(time.Now())
	}
	callbacks := append([]func(){}, s.onShutdown...)
	s.mu.Unlock()
	for _, f := range callbacks {
		go f()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
