//go:build (!amd64 && !arm64) || purego

package scan

import "bytes"

func IndexByte(b []byte, c byte) int { return bytes.IndexByte(b, c) }
