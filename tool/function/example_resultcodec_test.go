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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultcodec"
)

type codecBashInput struct {
	Command string `json:"command"`
}

type codecBashOutput struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

func runCodecBash(_ context.Context, in codecBashInput) (codecBashOutput, error) {
	return codecBashOutput{ExitCode: 0, Output: "ran: " + in.Command}, nil
}

// ExampleWithResultCodec presents a tool's result to the model as XML instead of
// the default JSON, without writing callbacks or constructing model messages.
func ExampleWithResultCodec() {
	bash := function.NewFunctionTool(
		runCodecBash,
		function.WithName("bash"),
		function.WithDescription("run a bash command"),
		function.WithResultCodec(resultcodec.XML()),
	)
	fmt.Println(bash.Declaration().Name)
	// Output: bash
}

// ExampleWithResultCodec_custom binds a typed encoder so the model sees a
// business-specific observation format.
func ExampleWithResultCodec_custom() {
	codec := resultcodec.Custom(
		func(_ context.Context, out codecBashOutput) (string, error) {
			return fmt.Sprintf("exit=%d output=%s", out.ExitCode, out.Output), nil
		},
	)
	bash := function.NewFunctionTool(
		runCodecBash,
		function.WithName("bash"),
		function.WithDescription("run a bash command"),
		function.WithResultCodec(codec),
	)
	fmt.Println(bash.Declaration().Name)
	// Output: bash
}
