//go:build amd64 && !purego

package scan

import "golang.org/x/sys/cpu"

func IndexByte(b []byte, c byte) int {
	if len(b) >= 32 && cpu.X86.HasAVX2 {
		return indexByteAVX2(b, c)
	}
	return indexByteAsm(b, c)
}

//go:noescape
func indexByteAsm(b []byte, c byte) int

//go:noescape
func indexByteAVX2(b []byte, c byte) int
