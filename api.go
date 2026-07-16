package httpgo

import (
	"bufio"
	"net"
)

type Handler interface {
	ServeHTTP(ResponseWriter, *Request)
}

type HandlerFunc func(ResponseWriter, *Request)

func (f HandlerFunc) ServeHTTP(w ResponseWriter, r *Request) { f(w, r) }

type ResponseWriter interface {
	Header() *Header
	Write([]byte) (int, error)
	WriteString(string) (int, error)
	WriteHeader(statusCode int)
}

type Flusher interface{ Flush() }

type Hijacker interface {
	Hijack() (net.Conn, *bufio.ReadWriter, error)
}

type Pusher interface {
	Push(target string, opts *PushOptions) error
}
