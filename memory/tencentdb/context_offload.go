//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var _ plugin.Plugin = (*contextOffloadPlugin)(nil)

const contextOffloadPluginName = "tencentdb_context_offload"

// ContextOffloadPlugin returns a runner plugin for short-term tool result
// offload. It is separate from Plugin so long-term recall does not
// unexpectedly rewrite tool result history.
func (s *Service) ContextOffloadPlugin() plugin.Plugin {
	if s == nil {
		return &contextOffloadPlugin{opts: defaultOptions()}
	}
	return &contextOffloadPlugin{opts: s.opts}
}

// NewContextOffloadPlugin creates a standalone context offload plugin.
func NewContextOffloadPlugin(opts ...Option) plugin.Plugin {
	options := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return &contextOffloadPlugin{opts: options}
}

type contextOffloadPlugin struct {
	opts Options
}

func (p *contextOffloadPlugin) Name() string {
	return contextOffloadPluginName
}

func (p *contextOffloadPlugin) Register(r *plugin.Registry) {
	if p == nil || !p.opts.ContextOffload.Enabled {
		return
	}
	r.AfterToolMessages(p.afterToolMessages)
	r.BeforeModel(p.beforeModel)
}

func (p *contextOffloadPlugin) afterToolMessages(
	ctx context.Context,
	args *plugin.AfterToolMessagesArgs,
) (*plugin.AfterToolMessagesResult, error) {
	if p == nil || args == nil || args.Invocation == nil ||
		args.Invocation.Session == nil {
		return nil, nil
	}
	sess := args.Invocation.Session
	if err := validateSessionScope(sess); err != nil {
		return nil, nil
	}
	store := newOffloadStorageContext(p.opts, sess, args.Invocation.AgentName)
	if err := ensureOffloadDirs(store); err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: ensure dirs failed: %v", err)
		return nil, nil
	}
	state, err := readOffloadState(store)
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: read state failed: %v", err)
		state = newOffloadState()
	}
	state.LastSessionKey = store.SessionKey
	registerOffloadSession(store)

	pairs, err := p.collectToolPairs(store, args)
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: collect tool pairs failed: %v", err)
		return nil, nil
	}
	if len(pairs) == 0 {
		return nil, nil
	}

	entries := p.summarizeToolPairs(ctx, args.Messages, pairs)
	if len(entries) == 0 {
		return nil, nil
	}
	if err := appendOffloadEntries(store, entries); err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: append entries failed: %v", err)
		return nil, nil
	}
	for _, entry := range entries {
		state.LastOffloadedToolCallID = entry.ToolCallID
		state.addConfirmed(entry.ToolCallID)
	}
	if err := p.advanceOffloadState(ctx, store, state, args.Messages); err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: advance state failed: %v", err)
	}
	if err := writeOffloadState(store, state); err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: write state failed: %v", err)
	}

	if p.opts.ContextOffload.Mode == ContextOffloadModeCollect {
		return nil, nil
	}
	return replaceCurrentToolResults(args.ToolResultMessages, entries), nil
}

func (p *contextOffloadPlugin) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if p == nil || args == nil || args.Request == nil {
		return nil, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil, nil
	}
	if err := validateSessionScope(inv.Session); err != nil {
		return nil, nil
	}
	store := newOffloadStorageContext(p.opts, inv.Session, inv.AgentName)
	if err := ensureOffloadDirs(store); err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: ensure dirs failed: %v", err)
		return nil, nil
	}
	state, err := readOffloadState(store)
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: read state failed: %v", err)
		state = newOffloadState()
	}
	state.LastSessionKey = store.SessionKey
	registerOffloadSession(store)

	entries, err := readAllOffloadEntries(store)
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: read index failed: %v", err)
		return nil, nil
	}
	if len(entries) == 0 {
		return nil, nil
	}
	if err := p.advanceOffloadState(ctx, store, state, args.Request.Messages); err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: advance state failed: %v", err)
	}
	entries, err = readAllOffloadEntries(store)
	if err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: reload index failed: %v", err)
		return nil, nil
	}
	if p.opts.ContextOffload.Mode != ContextOffloadModeCollect {
		history := p.applyL3(ctx, store, state, args.Request, entries)
		if err := injectOffloadContext(args.Request, store, state, history, p.opts); err != nil {
			log.WarnfContext(ctx, "tencentdb context offload: inject mmd failed: %v", err)
		}
	}
	if err := writeOffloadState(store, state); err != nil {
		log.WarnfContext(ctx, "tencentdb context offload: write state failed: %v", err)
	}
	return nil, nil
}

func (p *contextOffloadPlugin) collectToolPairs(
	store offloadStorageContext,
	args *plugin.AfterToolMessagesArgs,
) ([]offloadToolPair, error) {
	if args == nil {
		return nil, nil
	}
	minBytes := p.opts.ContextOffload.L0.MinToolResultBytes
	if minBytes <= 0 {
		minBytes = defaultContextOffloadMinToolResultBytes
	}
	calls := toolCallsByID(args.ToolCalls)
	pairs := make([]offloadToolPair, 0, len(args.ToolResultMessages))
	for _, msg := range args.ToolResultMessages {
		resultText := offloadToolResultText(msg)
		if len([]byte(resultText)) < minBytes {
			continue
		}
		call := calls[msg.ToolID]
		if call.ID == "" {
			call.ID = msg.ToolID
		}
		if call.Function.Name == "" {
			call.Function.Name = msg.ToolName
		}
		ref, err := writeOffloadRef(store, call, msg)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, offloadToolPair{
			ToolName:   toolCallName(call, msg),
			ToolCallID: msg.ToolID,
			Params:     string(call.Function.Arguments),
			Result:     resultText,
			ResultRef:  ref,
			Timestamp:  time.Now().Format(time.RFC3339Nano),
		})
	}
	return pairs, nil
}

func (p *contextOffloadPlugin) summarizeToolPairs(
	ctx context.Context,
	messages []model.Message,
	pairs []offloadToolPair,
) []offloadIndexEntry {
	if len(pairs) == 0 {
		return nil
	}
	maxPairs := p.opts.ContextOffload.L1.MaxPairsPerBatch
	if maxPairs <= 0 {
		maxPairs = defaultContextOffloadMaxPairsPerBatch
	}
	if maxPairs >= len(pairs) {
		return p.summarizeToolPairBatch(ctx, messages, pairs)
	}
	entries := make([]offloadIndexEntry, 0, len(pairs))
	for start := 0; start < len(pairs); start += maxPairs {
		end := start + maxPairs
		if end > len(pairs) {
			end = len(pairs)
		}
		entries = append(entries, p.summarizeToolPairBatch(ctx, messages, pairs[start:end])...)
	}
	return entries
}

func (p *contextOffloadPlugin) summarizeToolPairBatch(
	ctx context.Context,
	messages []model.Message,
	pairs []offloadToolPair,
) []offloadIndexEntry {
	client := p.newOffloadModelClient()
	recent := recentMessagesText(messages, 6)
	if client != nil {
		entries, err := client.L1Summarize(ctx, offloadL1Request{
			RecentMessages: recent,
			ToolPairs:      pairs,
		})
		if err == nil && len(entries) > 0 {
			return normalizeL1Entries(entries, pairs)
		}
		if err != nil && !errors.Is(err, errOffloadModelUnavailable) {
			log.WarnfContext(ctx, "tencentdb context offload: L1 failed: %v", err)
		}
	}
	entries := make([]offloadIndexEntry, 0, len(pairs))
	for _, pair := range pairs {
		entries = append(entries, fallbackL1Entry(pair))
	}
	return entries
}

func (p *contextOffloadPlugin) advanceOffloadState(
	ctx context.Context,
	store offloadStorageContext,
	state *offloadState,
	messages []model.Message,
) error {
	if state == nil {
		return nil
	}
	entries, err := readAllOffloadEntries(store)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if err := p.ensureTaskBoundary(ctx, store, state, messages, entries); err != nil {
		return err
	}
	return p.maybeRunL2(ctx, store, state, messages, entries)
}

func (p *contextOffloadPlugin) ensureTaskBoundary(
	ctx context.Context,
	store offloadStorageContext,
	state *offloadState,
	messages []model.Message,
	entries []offloadIndexEntry,
) error {
	start := firstUnjudgedBoundaryStart(state, entries)
	if start < 0 {
		return nil
	}
	judgment := fallbackTaskJudgment(messages)
	client := p.newOffloadModelClient()
	if client != nil {
		current, _ := readActiveMMD(store, state)
		metas, _ := listMMDMetas(store)
		got, err := client.L15Judge(ctx, offloadL15Request{
			RecentMessages:    recentMessagesText(messages, 6),
			CurrentMMD:        current,
			AvailableMMDMetas: metas,
		})
		if err == nil {
			judgment = normalizeTaskJudgment(got, judgment)
		} else if !errors.Is(err, errOffloadModelUnavailable) {
			log.WarnfContext(ctx, "tencentdb context offload: L1.5 failed: %v", err)
		}
	}
	if !judgment.IsLongTask {
		state.Boundaries = append(state.Boundaries, offloadBoundary{
			StartIndex: start,
			Result:     offloadBoundaryShort,
		})
		if judgment.TaskCompleted {
			state.ActiveMMDFile = ""
			state.ActiveMMDID = ""
		}
		state.L15Settled = true
		state.LastL15JudgedToolCallID = entries[len(entries)-1].ToolCallID
		return nil
	}
	target := judgment.ContinuationMMDFile
	if strings.TrimSpace(target) == "" && judgment.IsContinuation {
		target = state.ActiveMMDFile
	}
	if strings.TrimSpace(target) == "" {
		state.MMDCounter++
		if state.MMDCounter <= 0 {
			state.MMDCounter = 1
		}
		target = fmt.Sprintf("%03d-%s.mmd", state.MMDCounter, safeTaskLabel(judgment.NewTaskLabel))
	}
	state.ActiveMMDFile = target
	state.ActiveMMDID = strings.TrimSuffix(target, ".mmd")
	state.Boundaries = append(state.Boundaries, offloadBoundary{
		StartIndex: start,
		Result:     offloadBoundaryLong,
		TargetMMD:  target,
	})
	state.L15Settled = true
	state.LastL15JudgedToolCallID = entries[len(entries)-1].ToolCallID
	return nil
}

func firstUnjudgedBoundaryStart(state *offloadState, entries []offloadIndexEntry) int {
	if state == nil || len(entries) == 0 {
		return -1
	}
	if state.LastL15JudgedToolCallID == "" {
		return nextBoundaryStart(state, entries)
	}
	for i, entry := range entries {
		if entry.ToolCallID == state.LastL15JudgedToolCallID {
			if i+1 >= len(entries) {
				return -1
			}
			return i + 1
		}
	}
	return nextBoundaryStart(state, entries)
}

func nextBoundaryStart(state *offloadState, entries []offloadIndexEntry) int {
	start := 0
	for _, boundary := range state.Boundaries {
		if boundary.StartIndex >= start {
			start = boundary.StartIndex + 1
		}
	}
	if start >= len(entries) {
		return -1
	}
	return start
}

func (p *contextOffloadPlugin) maybeRunL2(
	ctx context.Context,
	store offloadStorageContext,
	state *offloadState,
	messages []model.Message,
	entries []offloadIndexEntry,
) error {
	target := state.ActiveMMDFile
	if target == "" {
		return nil
	}
	eligible := eligibleL2Entries(state, entries)
	if len(eligible) == 0 {
		return nil
	}
	runL2, err := p.shouldRunL2(store, state, eligible)
	if err != nil {
		return err
	}
	if !runL2 {
		return nil
	}
	existing, err := readMMD(store, target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	req := offloadL2Request{
		ExistingMMD:   existing,
		NewEntries:    eligible,
		RecentHistory: recentMessagesText(messages, 6),
		CurrentTurn:   latestUserMessageText(messages),
		TaskLabel:     strings.TrimSuffix(target, ".mmd"),
		MMDPrefix:     mmdPrefixFromFile(target),
		MMDCharCount:  len(existing),
	}
	client := p.newOffloadModelClient()
	var rsp offloadL2Response
	if client != nil {
		got, err := client.L2Generate(ctx, req)
		if err == nil {
			rsp = got
		} else if !errors.Is(err, errOffloadModelUnavailable) {
			log.WarnfContext(ctx, "tencentdb context offload: L2 failed: %v", err)
		}
	}
	if rsp.FileAction == "" {
		rsp = fallbackL2Response(req)
	}
	if err := applyL2Response(store, target, rsp); err != nil {
		return err
	}
	if err := backfillOffloadNodeIDs(store, rsp.NodeMapping, eligible); err != nil {
		return err
	}
	state.LastL2TriggerTime = time.Now().Format(time.RFC3339Nano)
	return nil
}

func (p *contextOffloadPlugin) shouldRunL2(
	store offloadStorageContext,
	state *offloadState,
	eligible []offloadIndexEntry,
) (bool, error) {
	if len(eligible) == 0 {
		return false, nil
	}
	if state.ActiveMMDFile != "" {
		if _, err := readMMD(store, state.ActiveMMDFile); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return true, nil
			}
			return false, err
		}
	}
	threshold := p.opts.ContextOffload.L2.NullThreshold
	if threshold <= 0 {
		threshold = defaultContextOffloadL2NullThreshold
	}
	nullCount := 0
	for _, entry := range eligible {
		if entry.NodeID == nil {
			nullCount++
		}
	}
	if nullCount >= threshold {
		return true, nil
	}
	timeout := p.opts.ContextOffload.L2.Timeout
	if timeout <= 0 {
		timeout = defaultContextOffloadL2Timeout
	}
	if state.LastL2TriggerTime == "" {
		return false, nil
	}
	last, err := time.Parse(time.RFC3339Nano, state.LastL2TriggerTime)
	if err != nil {
		return true, nil
	}
	return time.Since(last) >= timeout, nil
}

func toolCallsByID(calls []model.ToolCall) map[string]model.ToolCall {
	out := make(map[string]model.ToolCall, len(calls))
	for _, call := range calls {
		if call.ID != "" {
			out[call.ID] = call
		}
	}
	return out
}

func toolCallName(call model.ToolCall, msg model.Message) string {
	if call.Function.Name != "" {
		return call.Function.Name
	}
	if msg.ToolName != "" {
		return msg.ToolName
	}
	return "tool"
}

func replaceCurrentToolResults(
	original []model.Message,
	entries []offloadIndexEntry,
) *plugin.AfterToolMessagesResult {
	byID := make(map[string]offloadIndexEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ToolCallID] = entry
	}
	replacements := make([]model.Message, len(original))
	copy(replacements, original)
	var changed bool
	for i := range replacements {
		entry, ok := byID[replacements[i].ToolID]
		if !ok {
			continue
		}
		replacements[i].Content = offloadedToolMessageContent(entry)
		replacements[i].ContentParts = nil
		changed = true
	}
	if !changed {
		return nil
	}
	return &plugin.AfterToolMessagesResult{ToolResultMessages: replacements}
}

func offloadedToolMessageContent(entry offloadIndexEntry) string {
	var b strings.Builder
	b.WriteString("Tool result has been externalized to TencentDB short-term context offload.\n\n")
	b.WriteString("node_id: ")
	b.WriteString(entry.displayNodeID())
	b.WriteString("\nresult_ref: ")
	b.WriteString(entry.ResultRef)
	b.WriteString("\ntool_call_id: ")
	b.WriteString(entry.ToolCallID)
	b.WriteString("\nscore: ")
	b.WriteString(fmt.Sprintf("%.1f", entry.Score))
	b.WriteString("\n\nSummary:\n")
	b.WriteString(entry.Summary)
	b.WriteString("\n\nUse tdai_read_offload_ref with result_ref when exact details are needed.")
	return b.String()
}

func currentInvocation(ctx context.Context) (*agent.Invocation, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil, errors.New("tencentdb memory tool: invocation session is required")
	}
	if err := validateSessionScope(inv.Session); err != nil {
		return nil, err
	}
	return inv, nil
}

func contextOffloadSessionDir(opts Options, sess *session.Session) string {
	return newOffloadStorageContext(opts, sess, "").DataDir
}

func contextOffloadSessionDirForAgent(
	opts Options,
	sess *session.Session,
	agentName string,
) string {
	return newOffloadStorageContext(opts, sess, agentName).DataDir
}
