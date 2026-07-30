package storagewriter

import (
	"context"

	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/storage"
)

// Context keys for passing non-serializable objects through graph execution.
type storageCtxKey struct{}

// WithStorage returns a context with the storage backend attached.
func WithStorage(ctx context.Context, s storage.Storage) context.Context {
	return context.WithValue(ctx, storageCtxKey{}, s)
}

// GetStorage retrieves the storage backend from context.
func GetStorage(ctx context.Context) storage.Storage {
	s, _ := ctx.Value(storageCtxKey{}).(storage.Storage)
	return s
}
