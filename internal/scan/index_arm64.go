//go:build arm64 && !purego

package scan

func IndexByte(b []byte, c byte) int { return indexByteAsm(b, c) }

//go:noescape
func indexByteAsm(b []byte, c byte) int
