//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummarycontext "trpc.group/trpc-go/trpc-agent-go/session/internal/summarycontext"
	isummaryscope "trpc.group/trpc-go/trpc-agent-go/session/internal/summaryscope"
)

type modelVisibleItemsContextKey struct{}

func modelVisibleViewForSession(
	ctx context.Context,
	sess *session.Session,
) (*summaryview.View, bool) {
	if sess == nil {
		return nil, false
	}
	view, ok := summaryview.FromContext(ctx)
	if !ok {
		return nil, false
	}
	filterKey := isummaryscope.GetScopeFilterKey(sess)
	if view.FilterKey != filterKey {
		return nil, false
	}
	if previousSummary, present := isummarycontext.PreviousSummary(ctx); present &&
		view.PreviousSummary != previousSummary {
		return nil, false
	}
	if view.SessionID != sess.ID &&
		sess.ID != view.SessionID+":"+filterKey {
		return nil, false
	}
	return view, true
}

func contextWithModelVisibleItems(
	ctx context.Context,
	indexes []int,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(
		ctx,
		modelVisibleItemsContextKey{},
		append([]int(nil), indexes...),
	)
}

func modelVisibleItemsFromContext(ctx context.Context) ([]int, bool) {
	if ctx == nil {
		return nil, false
	}
	indexes, ok := ctx.Value(modelVisibleItemsContextKey{}).([]int)
	if !ok || len(indexes) == 0 {
		return nil, false
	}
	return append([]int(nil), indexes...), true
}
