//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"fmt"

	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// ExternalNoteName is the caller-executed note tool name.
const ExternalNoteName = "external_note"

func newExternalNoteTool() agenttool.Tool {
	return function.NewFunctionTool(
		externalNoteNotImplemented,
		function.WithName(ExternalNoteName),
		function.WithDescription("Ask the caller to provide a plain text note for the given topic."),
	)
}

func externalNoteNotImplemented(_ context.Context, args externalNoteArgs) (externalNoteResult, error) {
	return externalNoteResult{}, fmt.Errorf("%s is executed by the caller for topic %q", ExternalNoteName, args.Topic)
}

type externalNoteArgs struct {
	Topic string `json:"topic" description:"The topic that needs an external note."`
}

type externalNoteResult struct {
	Note string `json:"note" description:"The plain text note returned by the caller."`
}
