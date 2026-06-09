//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	processorLatencySpanPromptRender   = "skills.prompt.render"
	processorLatencySpanRepositoryLoad = "skills.repository.load"
	processorLatencySpanSummaryCache   = "skills.summary.cache"
	processorLatencySpanSummaryCompute = "skills.summary.compute"
	processorLatencySpanStateScan      = "skills.state.scan"

	processorAttrSkillCacheHit       = "skills.cache_hit"
	processorAttrSkillCacheKnown     = "skills.cache_known"
	processorAttrSkillContextAware   = "skills.repository.context_aware"
	processorAttrSkillLoadedCount    = "skills.loaded_count"
	processorAttrSkillRenderedBytes  = "skills.rendered_bytes"
	processorAttrSkillSelectedCount  = "skills.selected_count"
	processorAttrSkillSummaryCount   = "skills.summary_count"
	processorAttrSkillToolResultMode = "skills.tool_result_mode"
)

type skillSummaryCacheReporter interface {
	SummaryCacheHit(ctx context.Context) (bool, bool)
}

func processorLatencyEnabled(inv *agent.Invocation) bool {
	return inv != nil &&
		inv.RunOptions.LatencyDiagnosticsEnabled &&
		!inv.RunOptions.DisableTracing
}

func startProcessorLatencySpan(
	ctx context.Context,
	inv *agent.Invocation,
	name string,
	attrs ...attribute.KeyValue,
) (context.Context, oteltrace.Span, bool) {
	if !processorLatencyEnabled(inv) {
		return ctx, noop.Span{}, false
	}
	ctx, span, started := itrace.StartSpan(ctx, inv, name)
	if started && len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	return ctx, span, started
}

func finishProcessorLatencySpan(
	span oteltrace.Span,
	started bool,
	err error,
) {
	if !started {
		return
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

func processorSkillRepositoryAttrs(
	repo skill.Repository,
) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Bool(
			processorAttrSkillContextAware,
			skill.IsContextAwareRepository(repo),
		),
	}
}

func processorSkillSummaryCacheAttrs(
	ctx context.Context,
	repo skill.Repository,
) []attribute.KeyValue {
	reporter, ok := repo.(skillSummaryCacheReporter)
	if !ok || reporter == nil {
		return []attribute.KeyValue{
			attribute.Bool(processorAttrSkillCacheKnown, false),
		}
	}
	hit, known := reporter.SummaryCacheHit(ctx)
	return []attribute.KeyValue{
		attribute.Bool(processorAttrSkillCacheKnown, known),
		attribute.Bool(processorAttrSkillCacheHit, hit),
	}
}
