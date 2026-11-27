//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package summarystate coordinates request-derived summary snapshots
// (summaryview and summaryfork) across provider-side request transformations.
package summarystate

import (
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryfork"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ApplyRequestTransform atomically rebases summaryview and refreshes
// summaryfork when change proves that request history was preserved on the
// given flow request; otherwise it invalidates both snapshots.
//
// Because summaryview.InvalidateBinding sets a monotonic per-call tombstone
// that causes subsequent RebaseAfterTransform calls to return false, an
// earlier unsafe transform cannot be undone by a later preserved transform.
func ApplyRequestTransform(
	inv *agent.Invocation,
	change modelrequest.TokenTailoringChange,
	request *model.Request,
	buildForkRequest func() *model.Request,
) bool {
	if canRebaseRequestTransform(change, request) &&
		summaryview.RebaseAfterTransform(
			inv,
			change.Preserved.Before,
			change.Preserved.After,
			change.Preserved.SourceIndexes,
		) {
		forkReq := request
		if buildForkRequest != nil {
			forkReq = buildForkRequest()
		}
		summaryfork.Attach(inv, forkReq)
		return true
	}
	summaryview.InvalidateBinding(inv)
	summaryfork.Invalidate(inv)
	return false
}

func canRebaseRequestTransform(
	change modelrequest.TokenTailoringChange,
	request *model.Request,
) bool {
	p := change.Preserved
	if request == nil || p == nil ||
		change.Record.Provenance != modelrequest.TokenTailoringProvenancePreserved ||
		change.Record.BeforeMessages != len(p.Before) ||
		change.Record.AfterMessages != len(p.After) {
		return false
	}
	if p.SourceIndexes == nil && len(p.Before) != len(p.After) {
		return false
	}
	return messagesMatchRequest(p.After, request.Messages)
}

// messagesMatchRequest verifies that the provider mutated the flow's own
// request rather than a cloned copy (for example, from a wrapper or callback),
// because summaryfork.Attach snapshots the flow request directly.
func messagesMatchRequest(
	after []model.Message,
	current []model.Message,
) bool {
	if len(after) != len(current) {
		return false
	}
	if len(after) == 0 || &after[0] == &current[0] {
		return true
	}
	return reflect.DeepEqual(after, current)
}
