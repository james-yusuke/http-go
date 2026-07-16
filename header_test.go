package httpgo

import (
	"context"
	"net/url"
	"reflect"
	"testing"
)

func TestHeader(t *testing.T) {
	var h Header
	h.Add("content-type", "a")
	h.Add("Content-Type", "b")
	if got := h.Get("CONTENT-TYPE"); got != "a" {
		t.Fatalf("Get = %q", got)
	}
	if got := h.Values("content-type"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("Values = %#v", got)
	}
	h.Set("content-type", "c")
	if got := h.Values("Content-Type"); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("Set = %#v", got)
	}
	c := h.Clone()
	h.Del("Content-Type")
	if c.Get("content-type") != "c" || h.Has("content-type") {
		t.Fatal("Clone did not own its entries")
	}
}

func TestRequestClone(t *testing.T) {
	r := &Request{Method: MethodGet, URL: &url.URL{Path: "/a"}}
	r.Header.Add("X-Test", "one")
	r.SetPathValue("id", "42")
	c := r.Clone(context.Background())
	r.Header.Set("X-Test", "two")
	r.URL.Path = "/b"
	r.SetPathValue("id", "7")
	if c.Header.Get("X-Test") != "one" || c.URL.Path != "/a" || c.PathValue("id") != "42" {
		t.Fatalf("clone changed: %#v", c)
	}
}
