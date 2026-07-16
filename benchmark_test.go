package httpgo

import (
	"bufio"
	"bytes"
	"io"
	"testing"
)

var staticRequest = []byte("GET /hello HTTP/1.1\r\nHost: example.test\r\n\r\n")

func BenchmarkParseRequest(b *testing.B) {
	var source bytes.Reader
	cs := &connState{
		server:     &Server{},
		br:         bufio.NewReaderSize(&source, defaultIOBufferSize),
		remoteAddr: "127.0.0.1:1234",
		headerBuf:  make([]byte, 0, 4096),
	}
	for range 2 {
		source.Reset(staticRequest)
		cs.br.Reset(&source)
		cs.reqLife.activate()
		if err := cs.readRequest(); err != nil {
			b.Fatal(err)
		}
		cs.reqLife.release()
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(staticRequest)))
	b.ResetTimer()
	for range b.N {
		source.Reset(staticRequest)
		cs.br.Reset(&source)
		cs.reqLife.activate()
		if err := cs.readRequest(); err != nil {
			b.Fatal(err)
		}
		cs.reqLife.release()
	}
}

func BenchmarkBufferedResponse(b *testing.B) {
	cs := &connState{server: &Server{}, bw: bufio.NewWriterSize(io.Discard, defaultIOBufferSize)}
	cs.req.Method = MethodGet
	cs.req.Proto = "HTTP/1.1"
	cs.req.ProtoMinor = 1
	for range 2 {
		cs.res.reset(cs)
		_, _ = cs.res.WriteString("hello")
		if err := cs.res.finish(true); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		cs.res.reset(cs)
		_, _ = cs.res.WriteString("hello")
		if err := cs.res.finish(true); err != nil {
			b.Fatal(err)
		}
	}
}
