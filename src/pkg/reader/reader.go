package reader

import (
	"errors"
	"io"
	"sync"
)

const (
	defaultBufferSize = 1024
)

var (
	ErrOutOfBuffer = errors.New("n is bigger than len of buffer")

	pool = sync.Pool{New: func() any { return make([]byte, defaultBufferSize) }}
)

type BufferedReader struct {
	io.Reader
	buf  []byte
	l, r int
}

func New(rd io.Reader) *BufferedReader {
	return &BufferedReader{
		Reader: rd,
		buf:    pool.Get().([]byte),
	}
}

func (b *BufferedReader) ReadN(n int) ([]byte, error) {
	if n > len(b.buf)-b.r {
		return nil, ErrOutOfBuffer
	}
	b.l = b.r
	return b.readN(n, b.l)
}

func (b *BufferedReader) readN(n, l int) ([]byte, error) {
	c, err := b.Read(b.buf[l : l+n])
	b.r += c
	if err != nil {
		return nil, err
	}
	if c < n {
		return b.readN(n-c, b.r)
	}
	return b.buf[b.l:b.r], nil
}

func (b *BufferedReader) ReadByte() (byte, error) {
	buf, err := b.ReadN(1)
	if err != nil {
		return 0, err
	}
	return buf[0], nil
}

func (b *BufferedReader) Reset() {
	b.l = 0
	b.r = 0
}

func (b *BufferedReader) Cap() int {
	return len(b.buf)
}

func (b *BufferedReader) AllBytes() []byte {
	return b.buf[:b.r]
}

func (b *BufferedReader) LastBytes() []byte {
	return b.buf[b.l:b.r]
}

func (b *BufferedReader) Free() {
	b.Reset()
	//nolint:staticcheck
	pool.Put(b.buf)
	b.buf = nil
}
