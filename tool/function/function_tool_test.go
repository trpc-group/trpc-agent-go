package function_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestFunctionTool_Run_Success(t *testing.T) {
	type inputArgs struct {
		A int `json:"A"`
		B int `json:"B"`
	}
	type outputArgs struct {
		Result int `json:"result"`
	}
	fn := func(args inputArgs) outputArgs {
		return outputArgs{Result: args.A + args.B}
	}
	fTool := function.NewFunctionTool(fn,
		function.WithName("SumFunction"),
		function.WithDescription("Calculates the sum of two integers."))
	input := inputArgs{A: 2, B: 3}
	args := toArguments(t, input)

	result, err := fTool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sum, ok := result.(outputArgs)
	if !ok {
		t.Fatalf("expected int result, got %T", result)
	}
	if sum.Result != 5 {
		t.Errorf("expected 5, got %d", sum)
	}
}

// Helper function to create Arguments from any struct.
func toArguments(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	return json.RawMessage(b)
}

// Test input/output types for streaming functions
type streamTestInput struct {
	ArticalID int `json:"articalID,omitempty"`
}

type streamTestOutput struct {
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
	Reference string `json:"reference,omitempty"`
}

// Helper function to create a simple streaming function
func streamableFunc(input streamTestInput) *tool.StreamReader {
	stream := tool.NewStream(10)

	go func() {
		defer stream.Writer.Close()

		articals := []struct {
			Title     string
			Body      string
			Reference string
		}{
			{
				Title:     "Getting Started with Go",
				Body:      "Go is a programming language developed by Google. It's designed for simplicity, efficiency, and reliability. This guide will help you understand the basics of Go programming.",
				Reference: "https://golang.org/doc/tutorial/getting-started",
			},
			{
				Title:     "Understanding Goroutines",
				Body:      "Goroutines are lightweight threads managed by the Go runtime. They enable concurrent programming in Go, allowing you to run multiple functions simultaneously.",
				Reference: "https://golang.org/doc/effective_go#goroutines",
			},
			{
				Title:     "Working with Channels",
				Body:      "Channels are the pipes that connect concurrent goroutines. You can send values into channels from one goroutine and receive those values into another goroutine.",
				Reference: "https://golang.org/doc/effective_go#channels",
			},
		}

		id := input.ArticalID
		// send the article title
		output := streamTestOutput{
			Title: articals[id%len(articals)].Title,
		}
		chunk := tool.StreamChunk{
			Content:  output,
			Metadata: tool.Metadata{CreatedAt: time.Now()},
		}
		if closed := stream.Writer.Send(chunk, nil); closed {
			return
		}
		// Simulate processing delay
		time.Sleep(10 * time.Millisecond)

		// send the article body in two parts
		body := articals[id%len(articals)].Body
		output = streamTestOutput{
			Body: body[:len(body)/2],
		}
		chunk = tool.StreamChunk{
			Content:  output,
			Metadata: tool.Metadata{CreatedAt: time.Now()},
		}
		if closed := stream.Writer.Send(chunk, nil); closed {
			return
		}

		output = streamTestOutput{
			Body: body[len(body)/2:],
		}
		chunk = tool.StreamChunk{
			Content:  output,
			Metadata: tool.Metadata{CreatedAt: time.Now()},
		}
		if closed := stream.Writer.Send(chunk, nil); closed {
			return
		}

		// Simulate processing delay
		time.Sleep(10 * time.Millisecond)

		// send the article reference
		output = streamTestOutput{
			Reference: articals[id%len(articals)].Reference,
		}
		chunk = tool.StreamChunk{
			Content:  output,
			Metadata: tool.Metadata{CreatedAt: time.Now()},
		}
		if closed := stream.Writer.Send(chunk, nil); closed {
			return
		}

		// Simulate processing delay
		time.Sleep(10 * time.Millisecond)

	}()

	return stream.Reader
}

func Test_StreamableFunctionTool(t *testing.T) {
	st := function.NewStreamableFunctionTool[streamTestInput, streamTestOutput](streamableFunc,
		function.WithName("StreamableFunction"),
		function.WithDescription("Streams articles based on the provided article ID."))
	reader, err := st.StreamableCall(context.Background(), toArguments(t, streamTestInput{ArticalID: 1}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer reader.Close()
	var contents []any
	for {
		chunk, err := reader.Recv()
		if err != nil {
			if err == io.EOF {
				break // end of stream
			}
			t.Fatalf("unexpected error: %v", err)
		}
		contents = append(contents, chunk.Content)
		t.Logf("Received chunk: %+v", chunk.Content)
		t.Logf("Chunk metadata: %+v", chunk.Metadata)
	}
	mergedContent := tool.Merge(contents)
	bts, err := json.Marshal(mergedContent)
	if err != nil {
		t.Fatalf("failed to marshal output: %v", err)
	}

	expectedBts, err := json.Marshal(streamTestOutput{
		Title:     "Understanding Goroutines",
		Body:      "Goroutines are lightweight threads managed by the Go runtime. They enable concurrent programming in Go, allowing you to run multiple functions simultaneously.",
		Reference: "https://golang.org/doc/effective_go#goroutines",
	})
	if err != nil {
		t.Fatalf("failed to marshal expected output: %v", err)
	}
	if string(bts) != string(expectedBts) {
		t.Errorf("expected %s, got %s", expectedBts, bts)
	}
}
