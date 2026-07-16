//go:build httpgodebug

package httpgo

func checkLifetime(l *lifetime, gen uint64) {
	if l != nil && (!l.active || l.gen != gen) {
		panic("httpgo: borrowed value used after handler returned")
	}
}
