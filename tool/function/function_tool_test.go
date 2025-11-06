//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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
		A int `json:"A" jsonschema:"description=First integer operand,required"`
		B int `json:"B" jsonschema:"description=Second integer operand,required"`
	}
	type outputArgs struct {
		Result int `json:"result" jsonschema:"description=Sum of A and B"`
	}
	fn := func(_ context.Context, args inputArgs) (outputArgs, error) {
		return outputArgs{Result: args.A + args.B}, nil
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
	ArticleID int `json:"articleID,omitempty" jsonschema:"description=ID of the article to retrieve,enum=0,enum=1,enum=2"`
}

type streamTestOutput struct {
	Title     string `json:"title,omitempty" jsonschema:"description=Title of the article"`
	Body      string `json:"body,omitempty" jsonschema:"description=Content body of the article"`
	Reference string `json:"reference,omitempty" jsonschema:"description=Reference URL for the article"`
}

// Helper function to create a simple streaming function
func streamableFunc(ctx context.Context, input streamTestInput) (*tool.StreamReader, error) {
	stream := tool.NewStream(10)

	go func() {
		defer stream.Writer.Close()

		articles := []struct {
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

		id := input.ArticleID
		// send the article title
		output := streamTestOutput{
			Title: articles[id%len(articles)].Title,
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
		body := articles[id%len(articles)].Body
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
			Reference: articles[id%len(articles)].Reference,
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

	return stream.Reader, nil
}

func Test_StreamableFunctionTool(t *testing.T) {
	st := function.NewStreamableFunctionTool[streamTestInput, streamTestOutput](streamableFunc,
		function.WithName("StreamableFunction"),
		function.WithDescription("Streams articles based on the provided article ID."))
	reader, err := st.StreamableCall(context.Background(), toArguments(t, streamTestInput{ArticleID: 1}))
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

func TestFunctionTool_Declaration(t *testing.T) {
	type inputArgs struct {
		A int `json:"a" jsonschema:"description=First number,required"`
		B int `json:"b" jsonschema:"description=Second number,required"`
	}
	type outputArgs struct {
		Result int `json:"result" jsonschema:"description=Result of operation"`
	}

	fn := func(_ context.Context, args inputArgs) (outputArgs, error) {
		return outputArgs{Result: args.A + args.B}, nil
	}

	testCases := []struct {
		name            string
		opts            []function.Option
		expectedName    string
		expectedDesc    string
		expectNonNilIn  bool
		expectNonNilOut bool
	}{
		{
			name: "with name and description",
			opts: []function.Option{
				function.WithName("TestTool"),
				function.WithDescription("Test description"),
			},
			expectedName:    "TestTool",
			expectedDesc:    "Test description",
			expectNonNilIn:  true,
			expectNonNilOut: true,
		},
		{
			name:            "without options",
			opts:            nil,
			expectedName:    "",
			expectedDesc:    "",
			expectNonNilIn:  true,
			expectNonNilOut: true,
		},
		{
			name: "with name only",
			opts: []function.Option{
				function.WithName("NameOnlyTool"),
			},
			expectedName:    "NameOnlyTool",
			expectedDesc:    "",
			expectNonNilIn:  true,
			expectNonNilOut: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fTool := function.NewFunctionTool(fn, tc.opts...)
			decl := fTool.Declaration()

			if decl == nil {
				t.Fatal("Declaration() returned nil")
			}
			if decl.Name != tc.expectedName {
				t.Errorf("expected name %q, got %q", tc.expectedName, decl.Name)
			}
			if decl.Description != tc.expectedDesc {
				t.Errorf("expected description %q, got %q", tc.expectedDesc, decl.Description)
			}
			if tc.expectNonNilIn && decl.InputSchema == nil {
				t.Error("expected non-nil InputSchema")
			}
			if tc.expectNonNilOut && decl.OutputSchema == nil {
				t.Error("expected non-nil OutputSchema")
			}
		})
	}
}

func TestFunctionTool_LongRunning(t *testing.T) {
	type inputArgs struct {
		Value int `json:"value"`
	}
	type outputArgs struct {
		Result int `json:"result"`
	}

	fn := func(_ context.Context, args inputArgs) (outputArgs, error) {
		return outputArgs{Result: args.Value}, nil
	}

	testCases := []struct {
		name     string
		opts     []function.Option
		expected bool
	}{
		{
			name:     "default (not long running)",
			opts:     nil,
			expected: false,
		},
		{
			name: "explicitly set to true",
			opts: []function.Option{
				function.WithLongRunning(true),
			},
			expected: true,
		},
		{
			name: "explicitly set to false",
			opts: []function.Option{
				function.WithLongRunning(false),
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fTool := function.NewFunctionTool(fn, tc.opts...)
			if fTool.LongRunning() != tc.expected {
				t.Errorf("expected LongRunning() = %v, got %v", tc.expected, fTool.LongRunning())
			}
		})
	}
}

func TestFunctionTool_Call_UnmarshalError(t *testing.T) {
	type inputArgs struct {
		A int `json:"a"`
	}
	type outputArgs struct {
		Result int `json:"result"`
	}

	fn := func(_ context.Context, args inputArgs) (outputArgs, error) {
		return outputArgs{Result: args.A}, nil
	}

	fTool := function.NewFunctionTool(fn, function.WithName("TestTool"))

	// Invalid JSON
	invalidJSON := []byte(`{invalid json}`)
	_, err := fTool.Call(context.Background(), invalidJSON)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestStreamableFunctionTool_Declaration(t *testing.T) {
	testCases := []struct {
		name         string
		opts         []function.Option
		expectedName string
		expectedDesc string
	}{
		{
			name: "with name and description",
			opts: []function.Option{
				function.WithName("StreamTool"),
				function.WithDescription("Stream description"),
			},
			expectedName: "StreamTool",
			expectedDesc: "Stream description",
		},
		{
			name:         "without options",
			opts:         nil,
			expectedName: "",
			expectedDesc: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			st := function.NewStreamableFunctionTool[streamTestInput, streamTestOutput](
				streamableFunc,
				tc.opts...,
			)
			decl := st.Declaration()

			if decl == nil {
				t.Fatal("Declaration() returned nil")
			}
			if decl.Name != tc.expectedName {
				t.Errorf("expected name %q, got %q", tc.expectedName, decl.Name)
			}
			if decl.Description != tc.expectedDesc {
				t.Errorf("expected description %q, got %q", tc.expectedDesc, decl.Description)
			}
			if decl.InputSchema == nil {
				t.Error("expected non-nil InputSchema")
			}
			if decl.OutputSchema == nil {
				t.Error("expected non-nil OutputSchema")
			}
		})
	}
}

func TestStreamableFunctionTool_StreamableCall_UnmarshalError(t *testing.T) {
	st := function.NewStreamableFunctionTool[streamTestInput, streamTestOutput](
		streamableFunc,
		function.WithName("StreamTool"),
	)

	// Invalid JSON
	invalidJSON := []byte(`{invalid json}`)
	_, err := st.StreamableCall(context.Background(), invalidJSON)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestStreamableFunctionTool_StreamableCall_NilFunction(t *testing.T) {
	// Create a streamable tool with a function that returns nil
	nilFunc := func(ctx context.Context, input streamTestInput) (*tool.StreamReader, error) {
		return nil, nil
	}

	st := function.NewStreamableFunctionTool[streamTestInput, streamTestOutput](
		nilFunc,
		function.WithName("NilFuncTool"),
	)

	validJSON := toArguments(t, streamTestInput{ArticleID: 0})
	reader, err := st.StreamableCall(context.Background(), validJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reader != nil {
		t.Error("expected nil reader from nil function")
	}
}

func TestStreamableFunctionTool_LongRunning(t *testing.T) {
	// default false
	fn := func(_ context.Context, _ streamTestInput) (*tool.StreamReader, error) {
		return nil, nil
	}
	st1 := function.NewStreamableFunctionTool[streamTestInput, string](
		fn, function.WithName("st"),
	)
	if st1.LongRunning() {
		t.Errorf("expected default LongRunning=false")
	}
	st2 := function.NewStreamableFunctionTool[streamTestInput, string](
		fn, function.WithLongRunning(true),
	)
	if !st2.LongRunning() {
		t.Errorf("expected LongRunning=true")
	}
}

func TestFunctionTool_SkipSummarizationOption(t *testing.T) {
	type in struct {
		A int `json:"a"`
	}
	type out struct {
		B int `json:"b"`
	}
	fn := func(_ context.Context, x in) (out, error) {
		return out{B: x.A}, nil
	}

	// default false
	f1 := function.NewFunctionTool(fn)
	if f1.SkipSummarization() {
		t.Errorf("default SkipSummarization should be false")
	}

	// opt-in true
	f2 := function.NewFunctionTool(fn,
		function.WithSkipSummarization(true),
	)
	if !f2.SkipSummarization() {
		t.Errorf("expected SkipSummarization true")
	}
}

func TestStreamableFunctionTool_SkipSummarizationOption(t *testing.T) {
	fn := func(_ context.Context, _ streamTestInput) (*tool.StreamReader, error) {
		s := tool.NewStream(1)
		go func() {
			defer s.Writer.Close()
			_ = s.Writer.Send(tool.StreamChunk{Content: "x"}, nil)
		}()
		return s.Reader, nil
	}

	st1 := function.NewStreamableFunctionTool[streamTestInput, string](fn)
	if st1.SkipSummarization() {
		t.Errorf("default SkipSummarization should be false")
	}

	st2 := function.NewStreamableFunctionTool[streamTestInput, string](
		fn, function.WithSkipSummarization(true),
	)
	if !st2.SkipSummarization() {
		t.Errorf("expected SkipSummarization true")
	}
}
