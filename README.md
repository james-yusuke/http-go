# http-go

`http-go` is a server-focused HTTP implementation for Go. It provides a
`net/http`-shaped handler API while using a pooled, borrowed request model and
architecture-specific Plan 9 assembly in the HTTP/1 parser hot path.

> [!IMPORTANT]
> This project is pre-1.0. Audit and benchmark it against your workload before
> exposing it to untrusted production traffic.

## Features

- HTTP/1.0 and HTTP/1.1 with keep-alive, pipelining, chunked bodies and trailers
- HTTP/2 over TLS (ALPN), h2c prior knowledge, and h2c Upgrade
- Modern `ServeMux` patterns including methods, hosts, `{name}`, `{rest...}` and `{$}`
- Buffered small bodies and streaming large bodies
- Graceful shutdown, overload limits, flushing and connection hijacking
- AVX2 on amd64, NEON on arm64, and a `purego` fallback
- Strict rejection of ambiguous Content-Length / Transfer-Encoding requests

## Install

```sh
go get github.com/james-yusuke/http-go
```

Go 1.26 or newer is required.

## Quick start

```go
package main

import (
	"log"
	"time"

	httpgo "github.com/james-yusuke/http-go"
)

func main() {
	mux := httpgo.NewServeMux()
	mux.HandleFunc("GET /hello/{name}", func(w httpgo.ResponseWriter, r *httpgo.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.WriteString("hello " + r.PathValue("name"))
	})

	srv := &httpgo.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
```

TLS automatically advertises HTTP/2 through ALPN:

```go
log.Fatal(httpgo.ListenAndServeTLS(":8443", "cert.pem", "key.pem", mux))
```

Plaintext servers accept HTTP/1.1, HTTP/2 prior knowledge, and validated h2c
Upgrade requests on the same listener.

## Borrowed request lifetime

Incoming request strings, URL fields, and headers may refer to a reused
per-connection buffer. They are valid only until `ServeHTTP` returns. Clone a
request before storing it or sending it to another goroutine:

```go
owned := r.Clone(r.Context())
go processLater(owned)
```

Build with `-tags httpgodebug` during development to turn detected use-after-
handler access into a panic. `Header.Values` returns a newly allocated slice;
use `Header.Range` when iteration must stay allocation-free.

## `net/http` differences

- The package name is `httpgo`; it is not an import-only replacement for `net/http`.
- `ResponseWriter.Header()` returns `*httpgo.Header`, not `http.Header`.
- Request metadata is borrowed unless cloned.
- Client, form/multipart, file server, timeout handler, and automatic compression APIs are not included yet.
- HTTP/2 is implemented through `golang.org/x/net/http2`; its allocation profile differs from the custom HTTP/1 path.

## Limits and defaults

| Setting | Default |
| --- | ---: |
| `MaxHeaderBytes` | 1 MiB |
| `BodyBufferSize` | 64 KiB |
| `MaxRequestBodyBytes` | unlimited (`0`) |
| `MaxConnections` | 262,144 |
| `MaxConcurrency` | 262,144 |

Timeout zero values mean no timeout. Production servers should always set
explicit read-header, read, write, and idle timeouts.

## Test and benchmark

```sh
go test -race ./...
go test -tags purego ./...
go test -bench . -benchmem ./...

cd bench/compare
go test -bench HTTP1KeepAlive -benchmem -benchtime=10s -count=5
```

The comparison module uses one raw HTTP/1 client implementation for both
`httpgo` and `fasthttp`, avoiding mismatched client-side benchmark work. Run on
an otherwise idle machine and compare medians. Do not compare numbers copied
from different machines.

Current parser and response microbenchmarks enforce the intended steady-state
zero-allocation design, but end-to-end performance is hardware and workload
dependent and is not claimed without results from the comparison harness.

## License

MIT. See [LICENSE](LICENSE).
