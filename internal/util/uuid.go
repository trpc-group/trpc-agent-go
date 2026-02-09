package util

import (
	"encoding/binary"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var (
	uuidRandSeed atomic.Uint64
	uuidRandPool = sync.Pool{
		New: func() any {
			seed := int64(time.Now().UnixNano()) + int64(uuidRandSeed.Add(1))
			return rand.New(rand.NewSource(seed))
		},
	}
)

// NewUUIDString generates a UUIDv4 string without requiring crypto/rand on every call.
func NewUUIDString() string {
	r := uuidRandPool.Get().(*rand.Rand)
	var b [16]byte
	binary.LittleEndian.PutUint64(b[0:8], r.Uint64())
	binary.LittleEndian.PutUint64(b[8:16], r.Uint64())
	uuidRandPool.Put(r)

	id, _ := uuid.FromBytes(b[:])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id.String()
}
