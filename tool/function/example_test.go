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
	"encoding/xml"

	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultformat"
)

type commandInput struct {
	Command string `json:"command"`
}

type commandResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

func ExampleWithResultFormatter() {
	bashTool := function.NewFunctionTool(
		func(_ context.Context, input commandInput) (commandResult, error) {
			return commandResult{ExitCode: 0, Output: input.Command}, nil
		},
		function.WithName("bash"),
		function.WithDescription("Run a shell command"),
		function.WithResultFormatter(
			resultformat.FormatterFunc[commandResult](func(
				_ context.Context,
				result commandResult,
			) (string, error) {
				content, err := xml.Marshal(struct {
					XMLName  xml.Name `xml:"observation"`
					ExitCode int      `xml:"exit_code"`
					Output   string   `xml:"output"`
				}{
					ExitCode: result.ExitCode,
					Output:   result.Output,
				})
				return string(content), err
			}),
		),
	)

	_ = bashTool
}
