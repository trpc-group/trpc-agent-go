package processor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Test that when RunOptions.Messages is provided, the content processor
// uses them directly and does not mix in session-derived messages or
// invocation.Message, and still emits the preprocessing event.
func TestProcessRequest_UsesExplicitMessages(t *testing.T) {
	// Prepare explicit history and a session with events that would otherwise be included.
	explicit := []model.Message{
		model.NewSystemMessage("You are helpful"),
		model.NewUserMessage("hello"),
		model.NewAssistantMessage("hi!"),
		model.NewUserMessage("what can you do?"),
	}

	sess := &session.Session{}
	// Add a dummy event that would be included if not bypassed.
	sess.Events = append(sess.Events, *event.New("inv-1", "other-agent", event.WithResponse(&model.Response{
		Choices: []model.Choice{{Message: model.NewAssistantMessage("from session")}},
	})))

	inv := &agent.Invocation{
		InvocationID: "inv-1",
		AgentName:    "test-agent",
		Session:      sess,
		Message:      model.NewUserMessage("should be ignored when explicit provided"),
		RunOptions:   agent.RunOptions{Messages: explicit},
	}

	req := &model.Request{}
	ch := make(chan *event.Event, 1)
	p := NewContentRequestProcessor()

	p.ProcessRequest(context.Background(), inv, req, ch)

	// Ensure only explicit messages are used.
	require.Equal(t, len(explicit), len(req.Messages))
	for i := range explicit {
		require.Equal(t, explicit[i].Role, req.Messages[i].Role)
		require.Equal(t, explicit[i].Content, req.Messages[i].Content)
	}

	// Ensure a preprocessing event was emitted.
	select {
	case evt := <-ch:
		require.Equal(t, model.ObjectTypePreprocessingContent, evt.Object)
	default:
		t.Fatal("expected preprocessing event to be emitted")
	}
}
