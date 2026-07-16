// Package httpgo implements a server-focused HTTP stack with a net/http-like
// handler API and a low-allocation HTTP/1 hot path.
//
// Incoming Request, Header, and URL strings are borrowed from a per-connection
// buffer and are valid only until Handler.ServeHTTP returns. Call Request.Clone
// before retaining request metadata or passing it to another goroutine.
package httpgo
