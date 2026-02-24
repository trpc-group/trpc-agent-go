package telegram

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileOffsetStore_Read_Missing(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	offset, ok, err := store.Read(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, 0, offset)
}

func TestFileOffsetStore_WriteThenRead(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	require.NoError(t, store.Write(context.Background(), 123))

	offset, ok, err := store.Read(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 123, offset)
}

func TestFileOffsetStore_Read_InvalidJSON(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	require.NoError(t, os.WriteFile(p, []byte("{"), 0o600))

	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	_, _, err = store.Read(context.Background())
	require.Error(t, err)
}

func TestFileOffsetStore_Read_UnexpectedVersion(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	require.NoError(
		t,
		os.WriteFile(
			p,
			[]byte("{\"version\":999,\"offset\":1}"),
			0o600,
		),
	)

	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	_, _, err = store.Read(context.Background())
	require.Error(t, err)
}

func TestFileOffsetStore_Write_NegativeOffset(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "offset.json")
	store, err := NewFileOffsetStore(p)
	require.NoError(t, err)

	require.Error(t, store.Write(context.Background(), -1))
}
