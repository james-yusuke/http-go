package httpgo

import (
	"bufio"
	"errors"
	"net"
	"strconv"
	"strings"
)

type response struct {
	cs          *connState
	header      Header
	status      int
	wroteHeader bool
	streaming   bool
	hijacked    bool
	writeErr    error
	body        []byte
	scratch     []byte
	lengthBuf   [24]byte
	bodyBytes   int64
}

func (w *response) reset(cs *connState) {
	w.cs = cs
	w.header.reset(nil)
	w.status = 0
	w.wroteHeader = false
	w.streaming = false
	w.hijacked = false
	w.writeErr = nil
	w.body = w.body[:0]
	w.scratch = w.scratch[:0]
	w.bodyBytes = 0
}

func (w *response) Header() *Header { return &w.header }

func (w *response) WriteHeader(code int) {
	if w.wroteHeader || w.hijacked {
		return
	}
	if code < 100 || code > 999 {
		panic("httpgo: invalid WriteHeader code " + strconv.Itoa(code))
	}
	w.status = code
	w.wroteHeader = true
}

func (w *response) Write(p []byte) (int, error) {
	if w.hijacked {
		return 0, errors.New("httpgo: write after hijack")
	}
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if !w.wroteHeader {
		w.WriteHeader(StatusOK)
	}
	w.bodyBytes += int64(len(p))
	if responseHasNoBody(w.status) || w.cs.req.Method == MethodHead {
		return len(p), nil
	}
	if !w.streaming && len(w.body)+len(p) <= w.cs.server.responseBufferSize() {
		w.body = append(w.body, p...)
		return len(p), nil
	}
	if !w.streaming {
		if err := w.startStreaming(); err != nil {
			w.writeErr = err
			return 0, err
		}
		if len(w.body) > 0 {
			if err := w.writeChunk(w.body); err != nil {
				w.writeErr = err
				return 0, err
			}
			w.body = w.body[:0]
		}
	}
	if err := w.writeChunk(p); err != nil {
		w.writeErr = err
		return 0, err
	}
	return len(p), nil
}

func (w *response) WriteString(s string) (int, error) {
	if w.hijacked {
		return 0, errors.New("httpgo: write after hijack")
	}
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if !w.streaming && len(w.body)+len(s) <= w.cs.server.responseBufferSize() {
		if !w.wroteHeader {
			w.WriteHeader(StatusOK)
		}
		w.bodyBytes += int64(len(s))
		if responseHasNoBody(w.status) || w.cs.req.Method == MethodHead {
			return len(s), nil
		}
		w.body = append(w.body, s...)
		return len(s), nil
	}
	return w.Write([]byte(s))
}

func (w *response) Flush() {
	if w.hijacked || w.writeErr != nil {
		return
	}
	if !w.wroteHeader {
		w.WriteHeader(StatusOK)
	}
	if !w.streaming {
		if err := w.startStreaming(); err != nil {
			w.writeErr = err
			return
		}
		if len(w.body) > 0 {
			w.writeErr = w.writeChunk(w.body)
			w.body = w.body[:0]
		}
	}
	if w.writeErr == nil {
		w.writeErr = w.cs.bw.Flush()
	}
}

func (w *response) startStreaming() error {
	if w.streaming {
		return nil
	}
	w.streaming = true
	if w.cs.req.ProtoMinor == 0 {
		w.header.Set("Connection", "close")
		w.cs.closeAfterReply = true
	} else if !w.header.Has("Content-Length") && !responseHasNoBody(w.status) && w.cs.req.Method != MethodHead {
		w.header.Set("Transfer-Encoding", "chunked")
	}
	return w.writeHead()
}

func (w *response) writeChunk(p []byte) error {
	if len(p) == 0 || responseHasNoBody(w.status) || w.cs.req.Method == MethodHead {
		return nil
	}
	if w.cs.req.ProtoMinor == 1 && strings.EqualFold(w.header.Get("Transfer-Encoding"), "chunked") {
		w.scratch = strconv.AppendInt(w.scratch[:0], int64(len(p)), 16)
		if _, err := w.cs.bw.Write(w.scratch); err != nil {
			return err
		}
		if _, err := w.cs.bw.WriteString("\r\n"); err != nil {
			return err
		}
		if _, err := w.cs.bw.Write(p); err != nil {
			return err
		}
		_, err := w.cs.bw.WriteString("\r\n")
		return err
	}
	_, err := w.cs.bw.Write(p)
	return err
}

func (w *response) finish(flush bool) error {
	if w.hijacked {
		return nil
	}
	if !w.wroteHeader {
		w.WriteHeader(StatusOK)
	}
	if w.writeErr != nil {
		return w.writeErr
	}
	if !w.streaming {
		if !w.header.Has("Content-Length") && !w.header.Has("Transfer-Encoding") && !responseHasNoBody(w.status) {
			length := int64(len(w.body))
			if w.cs.req.Method == MethodHead {
				length = w.bodyBytes
			}
			formatted := strconv.AppendInt(w.lengthBuf[:0], length, 10)
			w.header.Set("Content-Length", bytesString(formatted))
		}
		if err := w.writeHead(); err != nil {
			return err
		}
		if w.cs.req.Method != MethodHead && !responseHasNoBody(w.status) && len(w.body) > 0 {
			if _, err := w.cs.bw.Write(w.body); err != nil {
				return err
			}
		}
	} else if w.cs.req.ProtoMinor == 1 && strings.EqualFold(w.header.Get("Transfer-Encoding"), "chunked") {
		if _, err := w.cs.bw.WriteString("0\r\n\r\n"); err != nil {
			return err
		}
	}
	if flush {
		return w.cs.bw.Flush()
	}
	return nil
}

func (w *response) writeHead() error {
	proto := w.cs.req.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	if w.cs.closeAfterReply && !w.header.Has("Connection") {
		w.header.Set("Connection", "close")
	}
	if !w.header.Has("Date") {
		w.header.Set("Date", appendHTTPDate())
	}
	w.scratch = append(w.scratch[:0], proto...)
	w.scratch = append(w.scratch, ' ')
	w.scratch = strconv.AppendInt(w.scratch, int64(w.status), 10)
	w.scratch = append(w.scratch, ' ')
	w.scratch = append(w.scratch, StatusText(w.status)...)
	w.scratch = append(w.scratch, '\r', '\n')
	w.header.Range(func(key, value string) bool {
		if !validResponseHeader(key, value) {
			return true
		}
		w.scratch = append(w.scratch, key...)
		w.scratch = append(w.scratch, ':', ' ')
		w.scratch = append(w.scratch, value...)
		w.scratch = append(w.scratch, '\r', '\n')
		return true
	})
	w.scratch = append(w.scratch, '\r', '\n')
	_, err := w.cs.bw.Write(w.scratch)
	return err
}

func validResponseHeader(key, value string) bool {
	return key != "" && !strings.ContainsAny(key, "\r\n:") && !strings.ContainsAny(value, "\r\n")
}

func responseHasNoBody(status int) bool {
	return status >= 100 && status <= 199 || status == StatusNoContent || status == 304
}

func (w *response) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.hijacked {
		return nil, nil, errors.New("httpgo: connection already hijacked")
	}
	if err := w.cs.bw.Flush(); err != nil {
		return nil, nil, err
	}
	w.hijacked = true
	return w.cs.conn, bufio.NewReadWriter(w.cs.br, w.cs.bw), nil
}

func (w *response) Push(string, *PushOptions) error { return ErrNotSupported }
