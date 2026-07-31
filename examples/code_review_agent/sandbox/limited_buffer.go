package sandbox

import "bytes"

type limitedBuffer struct {
	buffer    bytes.Buffer
	maxSize   int
	truncated bool
}

func newLimitedBuffer(maxSize int) *limitedBuffer {
	return &limitedBuffer{maxSize: maxSize}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.maxSize <= 0 {
		b.truncated = b.truncated || len(p) > 0
		return len(p), nil
	}
	remaining := b.maxSize - b.buffer.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buffer.Write(p[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buffer.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	result := b.buffer.String()
	if b.truncated {
		result += "\n... (truncated)"
	}
	return result
}

func (b *limitedBuffer) Truncated() bool {
	return b.truncated
}
