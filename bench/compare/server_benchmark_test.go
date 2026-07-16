package compare

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"

	httpgo "github.com/james-yusuke/http-go"
	"github.com/valyala/fasthttp"
)

var payload = bytes.Repeat([]byte{'x'}, 64)

func BenchmarkHTTP1KeepAlive(b *testing.B) {
	benchmarkServers(b, runRawClient)
}

func BenchmarkHTTP1Pipeline(b *testing.B) {
	benchmarkServers(b, runPipelinedClient)
}

func benchmarkServers(b *testing.B, run func(*testing.B, string)) {
	b.Helper()
	b.Run("httpgo", func(b *testing.B) {
		ln := listen(b)
		s := &httpgo.Server{Handler: httpgo.HandlerFunc(func(w httpgo.ResponseWriter, _ *httpgo.Request) {
			_, _ = w.Write(payload)
		})}
		go func() { _ = s.Serve(ln) }()
		b.Cleanup(func() { _ = s.Close() })
		run(b, ln.Addr().String())
	})

	b.Run("fasthttp", func(b *testing.B) {
		ln := listen(b)
		s := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
			_, _ = ctx.Write(payload)
		}}
		go func() { _ = s.Serve(ln) }()
		b.Cleanup(func() { _ = s.Shutdown() })
		run(b, ln.Addr().String())
	})
}

func runPipelinedClient(b *testing.B, addr string) {
	b.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReaderSize(conn, 64<<10)
	request := []byte("GET / HTTP/1.1\r\nHost: benchmark\r\n\r\n")
	const batchSize = 128
	batch := bytes.Repeat(request, batchSize)
	buffer := make([]byte, len(payload))
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for completed := 0; completed < b.N; {
		count := min(batchSize, b.N-completed)
		if _, err := conn.Write(batch[:count*len(request)]); err != nil {
			b.Fatal(err)
		}
		for range count {
			readResponse(b, br, buffer)
		}
		completed += count
	}
}

func listen(b *testing.B) net.Listener {
	b.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	return ln
}

func runRawClient(b *testing.B, addr string) {
	b.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReaderSize(conn, 16<<10)
	request := []byte("GET / HTTP/1.1\r\nHost: benchmark\r\n\r\n")
	buffer := make([]byte, len(payload))
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for range b.N {
		if _, err := conn.Write(request); err != nil {
			b.Fatal(err)
		}
		readResponse(b, br, buffer)
	}
}

func readResponse(b *testing.B, br *bufio.Reader, buffer []byte) {
	b.Helper()
	length := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			b.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			length, _ = strconv.Atoi(strings.TrimSpace(line[len("content-length:"):]))
		}
	}
	if length != len(buffer) {
		b.Fatalf("Content-Length = %d", length)
	}
	if _, err := io.ReadFull(br, buffer); err != nil {
		b.Fatal(err)
	}
}
