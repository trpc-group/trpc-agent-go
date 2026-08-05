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
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// exclusiveToolsNotice tells the model which tools cannot share a turn.
//
// It states the consequence rather than a rule, because the consequence is what
// makes the choice worth reasoning about: batching an exclusive tool with others
// does not fail, it silently costs the parallelism the rest of the batch would
// have had.
const exclusiveToolsNotice = "Tool-call batching: independent tool calls issued in the same turn %s, so a " +
	"turn of several calls can cost about as much as its slowest one. These tools are the exception and must " +
	"be the only call in their turn: %s. A turn that includes one of them runs every call in it one after " +
	"another."

// The clause describing what a turn's calls actually do depends on the run's
// concurrency configuration, which can withhold parallelism the scheduler would
// otherwise grant. Promising it unconditionally would misdescribe any run that
// configures a limit.
const (
	noticeRunsConcurrently = "run concurrently"
	noticeMayRunLimited    = "may run concurrently, up to this run's configured concurrency limits"
)

// ToolBatchingNotice annotates a request with the tools that must run alone.
//
// The framework already knows which those are — a tool publishes it, and the
// function-call processor refuses to run a turn concurrently unless every call in
// it is admissible — but that knowledge reaches the model nowhere. Without it the
// model cannot form a batch that actually parallelizes: pairing one exclusive
// tool with three cheap reads quietly serializes all four, and the model has no
// way to see why.
//
// It is deliberately not a flow.RequestProcessor. Preprocessing is too early:
// Flow.callLLM runs plugin and model before-model callbacks afterwards, and those
// callbacks receive a mutable Request whose Tools they add to and replace — the
// toolsearch plugin does exactly that. A notice computed during preprocessing can
// therefore omit a tool the model is shown, or keep naming one that was removed.
// This runs on the request as finalized, immediately before it reaches the model.
type ToolBatchingNotice struct {
	concurrency tool.ConcurrencyConfig
}

// NewToolBatchingNotice creates a notice for a run with the given tool
// concurrency configuration. Install it only when parallel tool execution is
// enabled; with it off, no tool can be batched and the notice would describe a
// capability the run does not have.
func NewToolBatchingNotice(concurrency tool.ConcurrencyConfig) *ToolBatchingNotice {
	return &ToolBatchingNotice{concurrency: concurrency}
}

// Annotate adds the notice to the finalized request's system message.
//
// It says nothing unless at least one available tool objects to sharing a turn,
// so a caller whose tools publish nothing sees an unchanged prompt.
func (n *ToolBatchingNotice) Annotate(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
) {
	if req == nil || len(req.Tools) < 2 {
		return
	}
	// An overall limit of one means nothing in the run ever runs beside anything
	// else. There is no parallelism left for an exclusive tool to cost, so the
	// notice would only describe a capability this run withholds from every tool.
	if n.concurrency.MaxConcurrency == 1 {
		return
	}
	exclusive := exclusiveToolNames(req.Tools)
	if len(exclusive) == 0 {
		return
	}
	appendToSystemMessage(req, n.notice(exclusive))
}

func (n *ToolBatchingNotice) notice(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, "`"+name+"`")
	}
	return fmt.Sprintf(
		exclusiveToolsNotice,
		n.concurrencyClause(),
		strings.Join(quoted, ", "),
	)
}

// concurrencyClause reports what a turn's calls actually do under this run's
// configuration. A group limit can serialize calls the scheduler admitted, so
// once any limit is configured the notice stops promising concurrency outright.
func (n *ToolBatchingNotice) concurrencyClause() string {
	if n.concurrency.MaxConcurrency > 0 {
		return noticeMayRunLimited
	}
	for _, group := range n.concurrency.Groups {
		if group.Limit > 0 && len(group.ToolNames) > 0 {
			return noticeMayRunLimited
		}
	}
	return noticeRunsConcurrently
}

// exclusiveToolNames lists, in a stable order, the tools that must run alone.
// The order is sorted rather than map order so the notice is byte-identical
// across requests and does not defeat prompt caching.
//
// It resolves through itool.IsConcurrencySafe for the same reason admission does:
// a host that patched a tool's declaration hands us an overlay wrapper that
// exposes none of the wrapped tool's interfaces, and the notice must name the
// same tools the scheduler will keep off the parallel path.
func exclusiveToolNames(tools map[string]tool.Tool) []string {
	var names []string
	for name, tl := range tools {
		if !itool.IsConcurrencySafe(tl) {
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
