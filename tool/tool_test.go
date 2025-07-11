package tool

import (
	"context"
	"testing"
	"time"
)

func TestStreamableTool_Interface(t *testing.T) {
	// Compile-time check
	var _ StreamableTool = (*testStreamableTool)(nil)
}

type testStreamableTool struct{}

func (d *testStreamableTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*StreamReader, error) {
	s := NewStream(1)
	go func() {
		defer s.Writer.Close()
		s.Writer.Send(StreamChunk{Content: "test", Metadata: Metadata{CreatedAt: time.Now()}}, nil)
		s.Writer.Send(StreamChunk{Content: "more data"}, nil)
		s.Writer.Send(StreamChunk{Content: "final chunk"}, nil)

	}()
	return s.Reader, nil
}
func (d *testStreamableTool) Declaration() *Declaration {
	return &Declaration{
		Name:        "TestStreamableTool",
		Description: "A test tool for streaming data.",
		InputSchema: &Schema{
			Type:        "object",
			Properties:  map[string]*Schema{"input": {Type: "string"}},
			Required:    []string{"input"},
			Description: "Input for the test streamable tool.",
		},
	}
}
