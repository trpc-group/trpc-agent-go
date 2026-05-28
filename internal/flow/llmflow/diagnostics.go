package llmflow

import (
	"context"
	"reflect"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	latencySpanEmitStartWait      = "llmflow.emit_start_event_wait"
	latencySpanSelectModel        = "llmflow.select_model"
	latencySpanPreprocess         = "llmflow.preprocess"
	latencySpanPreprocessStage    = "llmflow.preprocess.stage"
	latencySpanContextCheck       = "llmflow.context_compaction.check"
	latencySpanContextSummary     = "llmflow.context_compaction.create_summary"
	latencySpanContextRebuild     = "llmflow.context_compaction.rebuild"
	latencySpanResolveTools       = "llmflow.tools.resolve"
	latencyDiagnosticStageCompact = "context_compaction"
	latencyDiagnosticStatusStart  = "started"
	latencyDiagnosticStatusDone   = "completed"
	latencyDiagnosticStatusSkip   = "skipped"
	latencyDiagnosticStatusError  = "error"
)

func latencyDiagnosticsEnabled(inv *agent.Invocation) bool {
	return inv != nil && inv.RunOptions.LatencyDiagnosticsEnabled
}

func startLatencySpan(
	ctx context.Context,
	inv *agent.Invocation,
	name string,
	attrs ...attribute.KeyValue,
) (context.Context, oteltrace.Span, bool) {
	if !latencyDiagnosticsEnabled(inv) {
		return ctx, nil, false
	}
	ctx, span, started := itrace.StartSpan(ctx, inv, name)
	if started && len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	return ctx, span, started
}

func finishLatencySpan(span oteltrace.Span, started bool, err error) {
	if !started {
		return
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

func latencyRequestAttrs(req *model.Request) []attribute.KeyValue {
	if req == nil {
		return nil
	}
	return []attribute.KeyValue{
		attribute.Int("llmflow.request.messages", len(req.Messages)),
		attribute.Int("llmflow.request.tools", len(req.Tools)),
		attribute.Bool("llmflow.request.stream", req.GenerationConfig.Stream),
	}
}

func latencyProcessorName(processor any) string {
	if processor == nil {
		return ""
	}
	t := reflect.TypeOf(processor)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	name := t.String()
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

func emitLatencyDiagnosticEvent(
	ctx context.Context,
	inv *agent.Invocation,
	eventChan chan<- *event.Event,
	diagnostic event.LatencyDiagnostic,
) {
	if inv == nil || eventChan == nil ||
		!inv.RunOptions.LatencyDiagnosticsEnabled ||
		!inv.RunOptions.LatencyDiagnosticsEmitEvents {
		return
	}
	evt := event.New(
		inv.InvocationID,
		inv.AgentName,
		event.WithObject(model.ObjectTypePreprocessingStatus),
		event.WithExtension(
			event.LatencyDiagnosticExtensionKey,
			diagnostic,
		),
	)
	agent.EmitEvent(ctx, inv, eventChan, evt)
}

type contextCompactionDecision struct {
	shouldCompact bool
	tokenCount    int
	threshold     int
	contextWindow int
	err           error
}

func contextCompactionAttrs(
	decision contextCompactionDecision,
	req *model.Request,
) []attribute.KeyValue {
	attrs := latencyRequestAttrs(req)
	attrs = append(
		attrs,
		attribute.Bool(
			"llmflow.context_compaction.triggered",
			decision.shouldCompact,
		),
		attribute.Int(
			"llmflow.context_compaction.token_count",
			decision.tokenCount,
		),
		attribute.Int(
			"llmflow.context_compaction.threshold",
			decision.threshold,
		),
		attribute.Int(
			"llmflow.context_compaction.context_window",
			decision.contextWindow,
		),
	)
	return attrs
}
