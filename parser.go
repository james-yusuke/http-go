package httpgo

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
	"unsafe"

	"github.com/james-yusuke/http-go/internal/scan"
)

type parseError struct {
	status int
	msg    string
}

func (e *parseError) Error() string { return e.msg }

func perr(status int, msg string) error { return &parseError{status: status, msg: msg} }

func bytesString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}

func (cs *connState) readRequest() error {
	r := &cs.req
	r.pathValues = r.pathValues[:0]
	r.life = &cs.reqLife
	r.Header.reset(&cs.reqLife)
	r.Trailer.reset(&cs.reqLife)
	r.URL = &cs.url
	cs.url = url.URL{}
	r.Body = emptyRequestBody{}
	r.ContentLength = 0
	r.TLS = cs.tlsState
	r.RemoteAddr = cs.remoteAddr
	r.ctx = cs.ctx

	cs.headerBuf = cs.headerBuf[:0]
	limit := cs.server.maxHeaderBytes()
	for {
		line, err := cs.br.ReadSlice('\n')
		if len(cs.headerBuf)+len(line) > limit {
			return perr(StatusRequestHeaderFieldsTooLarge, "request header too large")
		}
		cs.headerBuf = append(cs.headerBuf, line...)
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if errors.Is(err, io.EOF) && len(cs.headerBuf) == 0 {
				return io.EOF
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return perr(StatusRequestTimeout, "request header timeout")
			}
			return perr(StatusBadRequest, "incomplete request header")
		}
		if len(line) == 2 && line[0] == '\r' && line[1] == '\n' {
			break
		}
		if len(line) == 1 && line[0] == '\n' {
			return perr(StatusBadRequest, "bare LF in request header")
		}
	}

	block := cs.headerBuf
	firstEnd := scan.IndexCRLF(block)
	if firstEnd <= 0 {
		return perr(StatusBadRequest, "malformed request line")
	}
	requestLine := block[:firstEnd]
	sp1 := scan.IndexByte(requestLine, ' ')
	if sp1 <= 0 {
		return perr(StatusBadRequest, "malformed request line")
	}
	sp2rel := scan.IndexByte(requestLine[sp1+1:], ' ')
	if sp2rel <= 0 {
		return perr(StatusBadRequest, "malformed request line")
	}
	sp2 := sp1 + 1 + sp2rel
	if scan.IndexByte(requestLine[sp2+1:], ' ') >= 0 {
		return perr(StatusBadRequest, "malformed request line")
	}
	r.Method = bytesString(requestLine[:sp1])
	if !scan.ValidToken(r.Method) {
		return perr(StatusBadRequest, "invalid method")
	}
	r.RequestURI = bytesString(requestLine[sp1+1 : sp2])
	if err := parseTarget(&cs.url, r.Method, r.RequestURI); err != nil {
		return perr(StatusBadRequest, "invalid request target")
	}
	r.Proto = bytesString(requestLine[sp2+1:])
	switch r.Proto {
	case "HTTP/1.1":
		r.ProtoMajor, r.ProtoMinor = 1, 1
	case "HTTP/1.0":
		r.ProtoMajor, r.ProtoMinor = 1, 0
	default:
		return perr(StatusBadRequest, "unsupported HTTP version")
	}

	remaining := block[firstEnd+2:]
	for len(remaining) > 2 {
		end := scan.IndexCRLF(remaining)
		if end < 0 {
			return perr(StatusBadRequest, "malformed header line")
		}
		line := remaining[:end]
		remaining = remaining[end+2:]
		if len(line) == 0 {
			break
		}
		if line[0] == ' ' || line[0] == '\t' {
			return perr(StatusBadRequest, "obsolete folded header")
		}
		colon := scan.IndexByte(line, ':')
		if colon <= 0 {
			return perr(StatusBadRequest, "malformed header field")
		}
		nameBytes := line[:colon]
		valueBytes := bytes.TrimSpace(line[colon+1:])
		name := bytesString(nameBytes)
		if !scan.ValidToken(name) || scan.HasCtl(valueBytes) {
			return perr(StatusBadRequest, "invalid header field")
		}
		value := bytesString(valueBytes)
		r.Header.entries = append(r.Header.entries, headerEntry{key: name, value: value})
	}

	host, hostCount := r.Header.firstAndCount("Host")
	if hostCount > 1 {
		return perr(StatusBadRequest, "multiple Host headers")
	}
	r.Host = host
	if r.ProtoMinor == 1 && r.Host == "" && r.Method != MethodConnect {
		return perr(StatusBadRequest, "missing Host header")
	}
	if r.URL.Host == "" {
		r.URL.Host = r.Host
	}

	contentLength, hasCL, err := parseContentLength(&r.Header)
	if err != nil {
		return err
	}
	te := r.Header.Values("Transfer-Encoding")
	chunked := false
	if len(te) > 0 {
		if hasCL {
			return perr(StatusBadRequest, "both Transfer-Encoding and Content-Length")
		}
		if len(te) != 1 || !strings.EqualFold(strings.TrimSpace(te[0]), "chunked") {
			return perr(StatusNotImplemented, "unsupported transfer encoding")
		}
		chunked = true
		contentLength = -1
	}
	if max := cs.server.MaxRequestBodyBytes; max > 0 && contentLength > max {
		return perr(StatusRequestEntityTooLarge, "request body too large")
	}
	r.ContentLength = contentLength
	cs.body.reset(cs, contentLength, chunked)
	if chunked || contentLength > 0 {
		expect := strings.TrimSpace(r.Header.Get("Expect"))
		if expect != "" {
			if !strings.EqualFold(expect, "100-continue") {
				return perr(StatusExpectationFailed, "unsupported expectation")
			}
			if _, err := cs.bw.WriteString("HTTP/1.1 100 Continue\r\n\r\n"); err != nil {
				return err
			}
			if err := cs.bw.Flush(); err != nil {
				return err
			}
		}
		if d := cs.server.ReadTimeout; d > 0 {
			_ = cs.conn.SetReadDeadline(time.Now().Add(d))
		}
		if !chunked && contentLength <= int64(cs.server.bodyBufferSize()) {
			if cap(cs.bodyBuf) < int(contentLength) {
				cs.bodyBuf = make([]byte, int(contentLength))
			}
			cs.bodyBuf = cs.bodyBuf[:int(contentLength)]
			if _, err := io.ReadFull(cs.br, cs.bodyBuf); err != nil {
				return perr(StatusBadRequest, "incomplete request body")
			}
			cs.body.setBuffered(cs.bodyBuf)
		}
		r.Body = &cs.body
	}
	return nil
}

func parseTarget(u *url.URL, method, target string) error {
	if target == "" || strings.ContainsAny(target, "\r\n\x00") {
		return errors.New("invalid target")
	}
	if method == MethodConnect && target[0] != '/' {
		u.Host = target
		return nil
	}
	if target == "*" {
		u.Path = "*"
		return nil
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		parsed, err := url.ParseRequestURI(target)
		if err != nil {
			return err
		}
		*u = *parsed
		return nil
	}
	if target[0] != '/' {
		return errors.New("origin-form target must begin with slash")
	}
	path := target
	if q := strings.IndexByte(target, '?'); q >= 0 {
		path, u.RawQuery = target[:q], target[q+1:]
	}
	u.RawPath = path
	if strings.IndexByte(path, '%') >= 0 {
		decoded, err := url.PathUnescape(path)
		if err != nil {
			return err
		}
		u.Path = decoded
	} else {
		u.Path = path
		u.RawPath = ""
	}
	return nil
}

func parseContentLength(h *Header) (int64, bool, error) {
	values := h.Values("Content-Length")
	if len(values) == 0 {
		return 0, false, nil
	}
	var parsed int64 = -1
	for _, line := range values {
		for _, part := range strings.Split(line, ",") {
			part = strings.TrimSpace(part)
			n, ok := scan.ParseDecimal(part)
			if !ok || parsed >= 0 && parsed != n {
				return 0, false, perr(StatusBadRequest, "invalid Content-Length")
			}
			parsed = n
		}
	}
	return parsed, true, nil
}

type emptyRequestBody struct{}

func (emptyRequestBody) Read([]byte) (int, error) { return 0, io.EOF }
func (emptyRequestBody) Close() error             { return nil }

type requestBody struct {
	cs           *connState
	remaining    int64
	chunked      bool
	chunkLeft    int64
	buffered     []byte
	off          int
	done         bool
	trailerBytes int
}

func (b *requestBody) reset(cs *connState, contentLength int64, chunked bool) {
	*b = requestBody{cs: cs, remaining: contentLength, chunked: chunked}
	if chunked {
		b.remaining = 0
	}
}

func (b *requestBody) setBuffered(p []byte) {
	b.buffered = p
	b.remaining = int64(len(p))
}

func (b *requestBody) Read(p []byte) (int, error) {
	if b.done {
		return 0, io.EOF
	}
	if len(b.buffered) > 0 {
		if b.off >= len(b.buffered) {
			b.done = true
			return 0, io.EOF
		}
		n := copy(p, b.buffered[b.off:])
		b.off += n
		b.remaining -= int64(n)
		return n, nil
	}
	if b.chunked {
		return b.readChunked(p)
	}
	if b.remaining <= 0 {
		b.done = true
		return 0, io.EOF
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.cs.br.Read(p)
	b.remaining -= int64(n)
	if err == io.EOF && b.remaining > 0 {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func (b *requestBody) readChunked(p []byte) (int, error) {
	for b.chunkLeft == 0 {
		line, err := readCRLFLine(b.cs.br, 4096)
		if err != nil {
			return 0, err
		}
		if semi := bytes.IndexByte(line, ';'); semi >= 0 {
			line = line[:semi]
		}
		n, ok := scan.ParseHex(strings.TrimSpace(bytesString(line)))
		if !ok {
			return 0, errors.New("httpgo: invalid chunk size")
		}
		if n == 0 {
			if err := b.readTrailers(); err != nil {
				return 0, err
			}
			b.done = true
			return 0, io.EOF
		}
		if max := b.cs.server.MaxRequestBodyBytes; max > 0 && b.remaining+n > max {
			return 0, errors.New("httpgo: request body too large")
		}
		b.chunkLeft = n
		b.remaining += n
	}
	if int64(len(p)) > b.chunkLeft {
		p = p[:b.chunkLeft]
	}
	n, err := io.ReadFull(b.cs.br, p)
	b.chunkLeft -= int64(n)
	if err != nil {
		return n, err
	}
	if b.chunkLeft == 0 {
		var crlf [2]byte
		if _, err := io.ReadFull(b.cs.br, crlf[:]); err != nil || crlf != [2]byte{'\r', '\n'} {
			return n, errors.New("httpgo: malformed chunk terminator")
		}
	}
	return n, nil
}

func (b *requestBody) readTrailers() error {
	for {
		line, err := readCRLFLine(b.cs.br, b.cs.server.maxHeaderBytes())
		if err != nil {
			return err
		}
		if len(line) == 0 {
			return nil
		}
		b.trailerBytes += len(line) + 2
		if b.trailerBytes > b.cs.server.maxHeaderBytes() {
			return errors.New("httpgo: trailers too large")
		}
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 {
			return errors.New("httpgo: malformed trailer")
		}
		name, value := string(line[:colon]), strings.TrimSpace(string(line[colon+1:]))
		if !scan.ValidToken(name) {
			return errors.New("httpgo: invalid trailer")
		}
		if strings.EqualFold(name, "Content-Length") || strings.EqualFold(name, "Transfer-Encoding") || strings.EqualFold(name, "Host") {
			return errors.New("httpgo: prohibited trailer")
		}
		b.cs.req.Trailer.Add(name, value)
	}
}

func (b *requestBody) Close() error {
	if b.done {
		return nil
	}
	var scratch [4096]byte
	for {
		_, err := b.Read(scratch[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func readCRLFLine(r *bufio.Reader, max int) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > max {
		return nil, errors.New("httpgo: line too large")
	}
	if err != nil {
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, errors.New("httpgo: bare LF")
	}
	return line[:len(line)-2], nil
}
