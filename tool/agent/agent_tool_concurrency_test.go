package agent

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// An agent tool runs a full agent loop that appends events to the session and
// reads them back, which the parallel path's frozen session clone hides from it —
// so it must never be batched, and tool.IsConcurrencySafe must see that through
// the interface rather than only via the concrete type.
func TestToolIsNotConcurrencySafe(t *testing.T) {
	at := &Tool{}
	if at.IsConcurrencySafe() {
		t.Error("an agent tool must not run on the parallel path")
	}
	if tool.IsConcurrencySafe(tool.Tool(at)) {
		t.Error("tool.IsConcurrencySafe must resolve the agent tool as unsafe")
	}
}
