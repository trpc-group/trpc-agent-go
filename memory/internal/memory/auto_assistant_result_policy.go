//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

const assistantResultPolicyName = "assistant-result-preserving"

// applyAssistantResultPolicy keeps independently useful assistant results from
// being collapsed by the default similarity policy. It deliberately reuses the
// public Preserve History classifier so both paths agree on identity, critical
// values, negation, and material token ordering.
func (w *AutoMemoryWorker) applyAssistantResultPolicy(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	if len(ops) == 0 {
		return nil
	}
	if w.updatePolicy == extractor.UpdatePolicyAppendOnly {
		return w.applyAppendOnlyPolicy(ctx, userKey, ops, existing)
	}

	byID := make(map[string]*memory.Entry, len(existing))
	for _, entry := range existing {
		if entry != nil && entry.Memory != nil && entry.ID != "" {
			byID[entry.ID] = entry
		}
	}
	out := make([]*extractor.Operation, 0, len(ops))
	for _, op := range ops {
		if op == nil {
			continue
		}
		switch op.Type {
		case extractor.OperationAdd:
			out = w.appendAssistantResultAdd(ctx, userKey, out, op, existing)
		case extractor.OperationUpdate:
			out = w.appendAssistantResultUpdate(ctx, userKey, out, op, byID[op.MemoryID])
		default:
			out = append(out, op)
		}
	}
	return out
}

func (w *AutoMemoryWorker) appendAssistantResultAdd(
	ctx context.Context,
	userKey memory.UserKey,
	out []*extractor.Operation,
	op *extractor.Operation,
	existing []*memory.Entry,
) []*extractor.Operation {
	if hasExactMemoryDuplicate(op, existing, out) {
		logAssistantResultDecision(ctx, userKey, op, nil, "no-op", "exact duplicate")
		return out
	}
	if !w.isToolEnabled(memory.AddToolName) {
		return append(out, op)
	}
	match := selectPreserveHistoryCandidate(op, existing)
	if match == nil {
		logAssistantResultDecision(ctx, userKey, op, nil, "add", "no safe candidate")
		return append(out, op)
	}
	if !w.isToolEnabled(memory.UpdateToolName) {
		logAssistantResultDecision(ctx, userKey, op, match, "add", "update tool disabled")
		return append(out, op)
	}
	logAssistantResultDecision(ctx, userKey, op, match, "update", "strict enrichment")
	return append(out, toUpdateOp(op, match.entry))
}

func (w *AutoMemoryWorker) appendAssistantResultUpdate(
	ctx context.Context,
	userKey memory.UserKey,
	out []*extractor.Operation,
	op *extractor.Operation,
	existing *memory.Entry,
) []*extractor.Operation {
	match := classifyPreserveHistoryCandidate(op, existing)
	if match != nil && match.duplicate {
		logAssistantResultDecision(ctx, userKey, op, match, "no-op", "exact duplicate")
		return out
	}
	if match != nil && w.isToolEnabled(memory.UpdateToolName) {
		logAssistantResultDecision(ctx, userKey, op, match, "update", "strict enrichment")
		return append(out, toUpdateOp(op, existing))
	}
	add := *op
	add.Type = extractor.OperationAdd
	add.MemoryID = ""
	logAssistantResultDecision(ctx, userKey, op, match, "add", "unsafe or unknown update")
	return append(out, &add)
}

func logAssistantResultDecision(
	ctx context.Context,
	userKey memory.UserKey,
	op *extractor.Operation,
	match *preserveHistoryCandidate,
	action string,
	reason string,
) {
	if match == nil {
		log.DebugfContext(ctx,
			"auto_memory: policy=%s action=%s reason=%s user=%s/%s operation=%s",
			assistantResultPolicyName, action, reason,
			userKey.AppName, userKey.UserID, op.Type,
		)
		return
	}
	log.DebugfContext(ctx,
		"auto_memory: policy=%s action=%s reason=%s user=%s/%s operation=%s candidate=%s old_coverage=%.3f new_coverage=%.3f",
		assistantResultPolicyName, action, reason,
		userKey.AppName, userKey.UserID, op.Type, match.entry.ID,
		match.oldCoverage, match.newCoverage,
	)
}
