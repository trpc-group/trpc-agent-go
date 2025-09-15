package llmflow

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

// noResponseModel returns a closed channel without emitting any responses.
type noResponseModel struct{}

func (m *noResponseModel) Info() model.Info { return model.Info{Name: "noresp"} }
func (m *noResponseModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
    ch := make(chan *model.Response)
    close(ch)
    return ch, nil
}

// Ensures Flow.Run does not panic when a step produces no events (lastEvent == nil).
// We use a short-lived context so the loop exits via ctx.Done() without hanging.
func TestRun_NoPanicWhenModelReturnsNoResponses(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
    defer cancel()

    f := New(nil, nil, Options{})
    inv := &agent.Invocation{InvocationID: "inv-nil", AgentName: "agent-nil", Model: &noResponseModel{}}

    ch, err := f.Run(ctx, inv)
    require.NoError(t, err)

    // Collect all events until channel closes. Expect none and, importantly, no panic.
    var count int
    for range ch {
        count++
    }
    require.Equal(t, 0, count)
}

