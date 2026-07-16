//go:build !httpgodebug

package httpgo

func checkLifetime(*lifetime, uint64) {}
