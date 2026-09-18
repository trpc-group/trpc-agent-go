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

// exclusiveToolsNotice tells the model which tools cannot share a turn. It states
// the consequence rather than a rule: batching an exclusive tool does not fail,
// it silently costs the parallelism the rest of the batch would have had.
const exclusiveToolsNotice = "Tool-call batching: independent tool calls issued in the " +
	"same turn %s, so a turn of several calls can cost about as much as its slowest one. These tools are the " +
	"exception and must be the only call in their turn: %s. A turn that includes one of them runs every call " +
	"in it one after another."

// noticeStart and noticeEnd delimit the span this annotator wrote, so a later
// pass can take back its own bytes and nothing else.
//
// Ownership cannot be inferred from how the notice reads: an earlier version
// matched its opening words, so a caller's own system policy beginning
// "Tool-call batching: " was deleted on the first pass. Nor can the edges be
// inferred from paragraph structure: a version that removed "the marked
// paragraph" had to normalize the caller's trailing newlines to find it, and
// withdrawing the notice then gave back trimmed content rather than the bytes
// the caller supplied. With both edges marked, the span and the separator
// written ahead of it are removed exactly.
const (
	noticeStart = "<!-- trpc-agent-go:tool-batching-notice -->"
	noticeEnd   = "<!-- /trpc-agent-go:tool-batching-notice -->"
)

// noticeSeparator is what appendToSystemMessage writes between the caller's
// content and the span, whatever that content ends in, so removal can take back
// exactly that.
const noticeSeparator = "\n\n"

// What a turn's calls actually do depends on the run's concurrency
// configuration, which can withhold parallelism the scheduler would otherwise
// grant. Promising it unconditionally would misdescribe any run with a limit.
const (
	noticeRunsConcurrently = "run concurrently"
	noticeMayRunLimited    = "may run concurrently, up to this run's configured concurrency limits"
)

// ToolBatchingNotice annotates a request with the tools that must run alone.
//
// The framework already knows which those are, but that knowledge reaches the
// model nowhere, so it cannot form a batch that actually parallelizes: pairing
// one exclusive tool with three cheap reads quietly serializes all four.
//
// It is deliberately not a flow.RequestProcessor. Before-model callbacks run
// after preprocessing and replace entries in Request.Tools — the toolsearch
// plugin does exactly that — so a notice computed there could omit a tool the
// model is shown, or keep naming one that was removed.
type ToolBatchingNotice struct {
	concurrency tool.ConcurrencyConfig
}

// NewToolBatchingNotice creates a notice for a run with the given tool
// concurrency configuration. Install it only when parallel tool execution is
// enabled; with it off, the notice describes a capability the run lacks.
func NewToolBatchingNotice(concurrency tool.ConcurrencyConfig) *ToolBatchingNotice {
	return &ToolBatchingNotice{concurrency: concurrency}
}

// Annotate adds the notice to the finalized request's system message.
//
// It says nothing unless some available tool objects to sharing a turn. That
// emptiness, not the number of tools offered, is the condition: a turn is a batch
// of calls rather than of definitions, and a model can call one function several
// times, so a sole exclusive tool still needs the notice.
//
// A retry re-runs the before-model callbacks over the same Request and this
// annotator runs again afterwards, by which point the tools may have changed. So
// annotating starts by removing whatever notice a previous pass left: the request
// carries the current one, or none once the tools that earned it are gone, and
// the caller's own system content is then byte for byte what it was before the
// first pass.
func (n *ToolBatchingNotice) Annotate(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
) {
	if req == nil {
		return
	}
	removeToolBatchingNotice(req)
	if len(req.Tools) == 0 {
		return
	}
	// An overall limit of one leaves no parallelism for an exclusive tool to
	// cost, so the notice would describe a capability withheld from every tool.
	if n.concurrency.MaxConcurrency == 1 {
		return
	}
	exclusive := exclusiveToolNames(req.Tools)
	if len(exclusive) == 0 {
		return
	}
	appendToSystemMessage(req, n.notice(exclusive))
}

// removeToolBatchingNotice deletes the notice a previous Annotate wrote, leaving
// the caller's own system content exactly as it was.
//
// It sweeps every system message, not the first: a callback may prepend one of
// its own, so on a retry the notice is no longer where appendToSystemMessage put
// it, and looking only at the first would leave it in place and add another
// beside it. A message that existed only to carry the notice is dropped.
func removeToolBatchingNotice(req *model.Request) {
	kept := req.Messages[:0]
	for _, msg := range req.Messages {
		if msg.Role != model.RoleSystem ||
			!strings.Contains(msg.Content, noticeStart) {
			kept = append(kept, msg)
			continue
		}
		content, ok := withoutNoticeSpans(msg.Content)
		if !ok {
			continue
		}
		msg.Content = content
		kept = append(kept, msg)
	}
	req.Messages = kept
}

// withoutNoticeSpans removes every delimited span from a system message's
// content, each together with the separator written ahead of it, and returns
// the rest byte for byte. The second result is false when the message existed
// only to carry the notice: nothing remains and no separator was ever written,
// which is the message appendToSystemMessage creates. A caller's empty system
// message that the notice was appended to is kept, empty, since the separator
// ahead of the span shows it was there first.
//
// Only a span with both edges is the annotator's; a start marker with no end is
// not removed, because its extent is unknown.
func withoutNoticeSpans(content string) (string, bool) {
	appended := false
	for {
		start := strings.Index(content, noticeStart)
		if start < 0 {
			break
		}
		length := strings.Index(content[start:], noticeEnd)
		if length < 0 {
			break
		}
		end := start + length + len(noticeEnd)
		if strings.HasSuffix(content[:start], noticeSeparator) {
			start -= len(noticeSeparator)
			appended = true
		}
		content = content[:start] + content[end:]
	}
	if content == "" && !appended {
		return "", false
	}
	return content, true
}

// notice renders the delimited span: the text between the markers, each marker
// on its own line so the model reads the sentence and not the fences.
func (n *ToolBatchingNotice) notice(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, "`"+name+"`")
	}
	return noticeStart + "\n" + fmt.Sprintf(
		exclusiveToolsNotice,
		n.concurrencyClause(),
		strings.Join(quoted, ", "),
	) + "\n" + noticeEnd
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

// exclusiveToolNames lists the tools that must run alone. Sorted rather than map
// order, so the notice is byte-identical across requests and does not defeat
// prompt caching.
//
// It resolves through itool.IsConcurrencySafe for the same reason admission does:
// a patched declaration arrives as an overlay exposing none of the wrapped tool's
// interfaces, and the notice must name the tools the scheduler actually keeps
// off the parallel path.
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
//
// The caller's content is left exactly as supplied, trailing newlines included,
// and the separator is written after it; removal takes back the separator and
// the span, so withdrawing the notice restores the caller's bytes. A trailing
// newline on the caller's side makes the seam wider than one blank line, which
// costs nothing, whereas trimming it changed content a caller may have loaded
// verbatim from a file and be caching by identity.
func appendToSystemMessage(req *model.Request, content string) {
	if idx := findSystemMessageIndex(req.Messages); idx >= 0 {
		req.Messages[idx].Content += noticeSeparator + content
		return
	}
	req.Messages = append(
		[]model.Message{model.NewSystemMessage(content)},
		req.Messages...,
	)
}
