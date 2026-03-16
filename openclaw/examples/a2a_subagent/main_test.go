package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWaitForReadyCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := waitForReady(ctx, "127.0.0.1:1", defaultA2ABase)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled))
}
