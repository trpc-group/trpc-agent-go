//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package resultcodec_test

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultcodec"
)

type exampleBashResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// ExampleJSON shows the default JSON encoding of a tool result.
func ExampleJSON() {
	out, _ := resultcodec.JSON().Encode(
		context.Background(),
		exampleBashResult{ExitCode: 0, Output: "done"},
	)
	fmt.Println(out)
	// Output: {"exit_code":0,"output":"done"}
}

// ExampleXML shows presenting the same result as XML.
func ExampleXML() {
	out, _ := resultcodec.XML().Encode(
		context.Background(),
		exampleBashResult{ExitCode: 0, Output: "done"},
	)
	fmt.Println(out)
	// Output: <result><exit_code>0</exit_code><output>done</output></result>
}

// ExampleText shows encoding an already-textual result verbatim.
func ExampleText() {
	out, _ := resultcodec.Text().Encode(
		context.Background(),
		"plain observation text",
	)
	fmt.Println(out)
	// Output: plain observation text
}

// ExampleCustom shows a typed encoder that formats the result with a
// business-specific template. The encoder receives the concrete result type, so
// no assertion from any is needed.
func ExampleCustom() {
	codec := resultcodec.Custom(
		func(_ context.Context, r exampleBashResult) (string, error) {
			return fmt.Sprintf("exit=%d\n%s", r.ExitCode, r.Output), nil
		},
	)
	out, _ := codec.Encode(
		context.Background(),
		exampleBashResult{ExitCode: 0, Output: "done"},
	)
	fmt.Println(out)
	// Output:
	// exit=0
	// done
}

// exampleWrapTool is a minimal tool whose construction we pretend cannot be
// modified, to demonstrate Wrap.
type exampleWrapTool struct{}

func (exampleWrapTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "bash", Description: "run a bash command"}
}

func (exampleWrapTool) Call(context.Context, []byte) (any, error) {
	return exampleBashResult{ExitCode: 0, Output: "done"}, nil
}

// ExampleWrap shows binding a codec to a tool you cannot reconstruct. The
// framework applies the codec to the tool's final result when building the
// model-visible message.
func ExampleWrap() {
	wrapped := resultcodec.Wrap(exampleWrapTool{}, resultcodec.XML())
	fmt.Println(wrapped.Declaration().Name)
	// Output: bash
}
