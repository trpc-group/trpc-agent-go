package session

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestWithMemory(t *testing.T) {
	// Create a test memory
	testMemory := memory.NewBaseMemory()

	// Create options with memory
	var opts Options
	withMemory := WithMemory(testMemory)
	withMemory(&opts)

	// Check the memory was set correctly
	if opts.Memory != testMemory {
		t.Errorf("Expected Memory to be set to testMemory, got different value")
	}
}
