//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// exclusiveToolsNotice tells the model which tools cannot share a turn.
//
// It states the consequence rather than a rule, because the consequence is what
// makes the choice worth reasoning about: batching an exclusive tool with others
// does not fail, it silently costs the parallelism the rest of the batch would
// have had.
const exclusiveToolsNotice = "Tool-call batching: independent tool calls issued in the same turn run " +
	"concurrently, so a turn of several calls costs about as much as its slowest one. These tools are the " +
	"exception and must be the only call in their turn: %s. A turn that includes one of them runs every call " +
	"in it one after another."

// ParallelToolsRequestProcessor tells the model which of the tools available to
// it cannot be batched with others.
//
// The framework already knows this — a tool publishes it, and the function-call
// processor refuses to run a turn concurrently unless every call in it is safe —
// but that knowledge reaches the model nowhere. Without it the model cannot form
// a batch that actually parallelizes: pairing one exclusive tool with three
// cheap reads quietly serializes all four, and the model has no way to see why.
type ParallelToolsRequestProcessor struct{}

// NewParallelToolsRequestProcessor creates a processor that annotates requests
// with the tools that must run alone. Install it only when parallel tool
// execution is enabled; with it off, no tool can be batched and the note would
// describe a capability the run does not have.
func NewParallelToolsRequestProcessor() *ParallelToolsRequestProcessor {
	return &ParallelToolsRequestProcessor{}
}

// ProcessRequest implements the flow.RequestProcessor interface.
//
// It says nothing unless at least one available tool publishes itself as
// concurrency-unsafe, so a caller whose tools publish no metadata sees an
// unchanged prompt.
func (p *ParallelToolsRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil || len(req.Tools) < 2 {
		return
	}
	exclusive := exclusiveToolNames(req.Tools)
	if len(exclusive) == 0 {
		return
	}
	appendToSystemMessage(req, exclusiveToolNotice(exclusive))
}

func exclusiveToolNotice(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, "`"+n+"`")
	}
	return fmt.Sprintf(exclusiveToolsNotice, strings.Join(quoted, ", "))
}

// exclusiveToolNames lists, in a stable order, the tools that must run alone.
// The order is sorted rather than map order so the note is byte-identical across
// requests and does not defeat prompt caching.
func exclusiveToolNames(tools map[string]tool.Tool) []string {
	var names []string
	for name, tl := range tools {
		if !tool.IsConcurrencySafe(tl) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// appendToSystemMessage adds content to the request's system message, creating
// one if the request has none.
func appendToSystemMessage(req *model.Request, content string) {
	if idx := findSystemMessageIndex(req.Messages); idx >= 0 {
		req.Messages[idx].Content += "\n\n" + content
		return
	}
	req.Messages = append(
		[]model.Message{model.NewSystemMessage(content)},
		req.Messages...,
	)
}
