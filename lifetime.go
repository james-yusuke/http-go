package httpgo

type lifetime struct {
	active bool
	gen    uint64
}

func (l *lifetime) activate() {
	l.gen++
	l.active = true
}

func (l *lifetime) release() { l.active = false }
