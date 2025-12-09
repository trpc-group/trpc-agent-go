package flush

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestInvokeFindsParentFlusher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parent := agent.NewInvocation()
	ch := make(chan *FlushRequest, 1)
	Attach(ctx, parent, ch)
	child := parent.Clone()

	done := make(chan error, 1)
	go func() {
		select {
		case req := <-ch:
			if req == nil || req.ACK == nil {
				done <- context.Canceled
				return
			}
			close(req.ACK)
			done <- nil
		case <-ctx.Done():
			done <- ctx.Err()
		}
	}()

	require.NoError(t, Invoke(ctx, child))
	require.NoError(t, <-done)
}

func TestInvokeNoFlusher(t *testing.T) {
	require.NoError(t, Invoke(context.Background(), agent.NewInvocation()))
}
