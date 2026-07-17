//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates per-tool result codecs: how the same tool result can
// be presented to the model as JSON, XML, plain text, or a custom template, and
// how to bind a codec per tool via function.WithResultCodec or resultcodec.Wrap.
//
// This example needs no API key: it prints codec output directly and shows the
// per-tool wiring. In a real agent, the framework applies the bound codec to the
// tool's final result when it builds the model-visible tool result message.
package main

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultcodec"
)

// bashInput is the tool argument type.
type bashInput struct {
	Command string `json:"command"`
}

// bashResult is a sample structured tool result.
type bashResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

func runBash(_ context.Context, in bashInput) (bashResult, error) {
	// The output contains <, >, & and quotes to show how each codec handles them.
	return bashResult{ExitCode: 0, Output: "ran " + in.Command + ": <ok> & \"done\""}, nil
}

func main() {
	ctx := context.Background()
	result := bashResult{ExitCode: 0, Output: "<ok> & \"done\""}

	fmt.Println("== Built-in codecs (same result, different model-visible formats) ==")
	printEncode(ctx, "JSON  ", resultcodec.JSON(), result)
	printEncode(ctx, "XML   ", resultcodec.XML(), result)
	printEncode(ctx, "Text  ", resultcodec.Text(), "plain observation text")

	// Custom typed encoder: the business template receives the concrete result
	// type, so there is no need to assert `any`.
	custom := resultcodec.Custom(func(_ context.Context, r bashResult) (string, error) {
		return fmt.Sprintf("exit=%d\n%s", r.ExitCode, r.Output), nil
	})
	printEncode(ctx, "Custom", custom, result)

	fmt.Println("\n== Per-tool configuration ==")

	// Choose XML for one function tool with a single option.
	xmlTool := function.NewFunctionTool(
		runBash,
		function.WithName("bash_xml"),
		function.WithDescription("run a bash command; result encoded as XML"),
		function.WithResultCodec(resultcodec.XML()),
	)
	fmt.Printf("function tool %q -> XML codec\n", xmlTool.Declaration().Name)

	// Use a typed custom encoder for another tool.
	customTool := function.NewFunctionTool(
		runBash,
		function.WithName("bash_custom"),
		function.WithDescription("run a bash command; result encoded by a custom template"),
		function.WithResultCodec(custom),
	)
	fmt.Printf("function tool %q -> Custom codec\n", customTool.Declaration().Name)

	// Leave a third tool on the default JSON behavior.
	jsonTool := function.NewFunctionTool(
		runBash,
		function.WithName("bash_json"),
		function.WithDescription("run a bash command; default JSON result"),
	)
	fmt.Printf("function tool %q -> default JSON (no codec)\n", jsonTool.Declaration().Name)

	// Bind a codec to a tool you cannot reconstruct (for example a tool produced
	// by a ToolSet) with resultcodec.Wrap.
	var existing tool.Tool = function.NewFunctionTool(
		runBash,
		function.WithName("bash_wrapped"),
		function.WithDescription("run a bash command"),
	)
	wrapped := resultcodec.Wrap(existing, resultcodec.XML())
	fmt.Printf("wrapped tool %q -> XML codec via resultcodec.Wrap\n", wrapped.Declaration().Name)

	fmt.Println("\nAdd these tools to an LLMAgent as usual. When the model calls a tool,")
	fmt.Println("the framework encodes its final result with the bound codec and sends it")
	fmt.Println("as the tool result message; tools without a codec keep the JSON default.")
}

func printEncode(ctx context.Context, label string, codec resultcodec.Codec, result any) {
	out, err := codec.Encode(ctx, result)
	if err != nil {
		fmt.Printf("%s -> error: %v\n", label, err)
		return
	}
	fmt.Printf("%s -> %s\n", label, out)
}
