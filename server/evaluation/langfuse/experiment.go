//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// APIVersion selects the Langfuse write API generation the handler targets.
type APIVersion string

const (
	// APIVersionV3 writes through the legacy public API: POST /api/public/traces
	// and POST /api/public/dataset-run-items. It is the default and the only
	// option for self-hosted Langfuse v3.
	APIVersionV3 APIVersion = "v3"
	// APIVersionV4 writes trace and experiment data as OpenTelemetry span
	// attributes. Langfuse v4 removes the trace write and deprecates
	// dataset-run-items; Langfuse Cloud is v4-only from 2026-11-16.
	APIVersionV4 APIVersion = "v4"
)

// Experiment and trace span attributes Langfuse v4 reads from the case's root span.
// Names follow Langfuse's own SDK (langfuse/_client/attributes.py).
const (
	attrTraceInput             = "langfuse.trace.input"
	attrTraceTags              = "langfuse.trace.tags"
	attrTraceMetadataPrefix    = "langfuse.trace.metadata."
	attrSessionID              = "langfuse.session.id"
	attrExperimentID           = "langfuse.experiment.id"
	attrExperimentName         = "langfuse.experiment.name"
	attrExperimentDesc         = "langfuse.experiment.description"
	attrExperimentMetadata     = "langfuse.experiment.metadata"
	attrExperimentDataset      = "langfuse.experiment.dataset.id"
	attrExperimentItemID       = "langfuse.experiment.item.id"
	attrExperimentRootID       = "langfuse.experiment.item.root_observation_id"
	attrExperimentItemMetadata = "langfuse.experiment.item.metadata"
)

func (v APIVersion) valid() bool {
	return v == APIVersionV3 || v == APIVersionV4
}

// experimentID derives the id Langfuse v4 uses to group the items of one
// dataset run. It must match the server's derivation byte for byte
// (SHA-256 of the JSON array ["langfuse-experiment-v1", project, dataset, run],
// first 16 hex characters), the same one Langfuse's Python SDK computes, so a
// re-run with the same run name lands in the same experiment and run-level
// scores posted with datasetRunId=<this id> attach to it.
func experimentID(projectID, datasetID, runName string) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	// JavaScript's JSON.stringify and Python's ensure_ascii=False leave <, > and &
	// and U+2028/U+2029 literal; encoding/json escapes them unless told otherwise.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode([]string{"langfuse-experiment-v1", projectID, datasetID, runName}); err != nil {
		return "", fmt.Errorf("encode experiment id payload: %w", err)
	}
	payload := strings.TrimSuffix(buffer.String(), "\n")
	payload = unescapeLineSeparators(payload)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])[:16], nil
}

// unescapeLineSeparators only decodes real JSON escapes, preserving a run name
// that contains the literal text "\\u2028" or "\\u2029".
func unescapeLineSeparators(payload string) string {
	var result strings.Builder
	for i := 0; i < len(payload); i++ {
		if payload[i] == '\\' && i+1 < len(payload) {
			if strings.HasPrefix(payload[i:], `\u2028`) {
				result.WriteRune('\u2028')
				i += 5
				continue
			}
			if strings.HasPrefix(payload[i:], `\u2029`) {
				result.WriteRune('\u2029')
				i += 5
				continue
			}
			result.WriteByte(payload[i])
			i++
		}
		result.WriteByte(payload[i])
	}
	return result.String()
}

// newExperimentID resolves the experiment id for one remote experiment request.
func newExperimentID(request *remoteExperimentRequest, runName string) (string, error) {
	if request == nil || strings.TrimSpace(request.ProjectID) == "" {
		return "", errors.New("projectId is required to derive the Langfuse v4 experiment id")
	}
	return experimentID(request.ProjectID, request.DatasetID, runName)
}

// v4CaseAttributes returns the span attributes that replace, for one case, the
// POST /api/public/traces and POST /api/public/dataset-run-items writes: the
// trace fields the trace upsert used to carry and the experiment association the
// run item used to create. They are set on the experiment item span, so they
// must be known before the case runs; the trace output is the root observation's
// own output in v4 and is no longer written separately.
func (h *Handler) v4CaseAttributes(spec *CaseSpec, opts executionOptions, sessionID string) ([]attribute.KeyValue, error) {
	attrs := []attribute.KeyValue{
		attribute.String(attrExperimentID, opts.experimentID),
		attribute.String(attrExperimentName, opts.runName),
		attribute.String(attrExperimentDataset, opts.datasetID),
		attribute.String(attrExperimentItemID, spec.DatasetItemID),
	}
	experimentMetadata, err := json.Marshal(map[string]any{"appName": h.appName, "runName": opts.runName})
	if err != nil {
		return nil, fmt.Errorf("encode experiment metadata: %w", err)
	}
	attrs = append(attrs, attribute.String(attrExperimentMetadata, string(experimentMetadata)))
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		attrs = append(attrs, attribute.String(attrSessionID, sessionID))
	}
	if len(opts.traceTags) > 0 {
		attrs = append(attrs, attribute.StringSlice(attrTraceTags, opts.traceTags))
	}
	input, err := stringifyValue("trace input", spec.TraceInput)
	if err != nil {
		return nil, err
	}
	if input != "" {
		attrs = append(attrs, attribute.String(attrTraceInput, input))
	}
	keys := make([]string, 0, len(spec.TraceMetadata))
	for key := range spec.TraceMetadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, err := stringifyValue("trace metadata "+key, spec.TraceMetadata[key])
		if err != nil {
			return nil, err
		}
		attrs = append(attrs, attribute.String(attrTraceMetadataPrefix+key, value))
	}
	return attrs, nil
}

type experimentSpanKey struct{}

// caseUserID returns the user a case runs as. Like resolveUserID on the v3
// path, the inference session user wins over the case user, so a CaseBuilder
// that sets a different SessionInput.UserID is reported under that user.
func caseUserID(spec *CaseSpec) string {
	if spec.EvalCase != nil && spec.EvalCase.SessionInput != nil &&
		strings.TrimSpace(spec.EvalCase.SessionInput.UserID) != "" {
		return spec.EvalCase.SessionInput.UserID
	}
	return spec.UserID
}

// startExperimentItem follows Langfuse's experiment task-span convention. The
// item span supplies a root observation ID before agent/tool spans start.
// The run description is set on the item span only, as Langfuse does not
// propagate it to child observations.
//
// It also returns the item-scoped attributes that only exist once the span has
// started (the root observation ID and the dataset item metadata). Callers set
// them on descendant spans directly, because Langfuse requires the root
// observation ID on every observation of an item trace and custom exporter
// setups do not install the baggage span processor.
func (h *Handler) startExperimentItem(ctx context.Context, spec *CaseSpec, attrs []attribute.KeyValue, description string) (context.Context, oteltrace.Span, []attribute.KeyValue, error) {
	startOpts := []oteltrace.SpanStartOption{oteltrace.WithNewRoot(), oteltrace.WithAttributes(attrs...)}
	if strings.TrimSpace(description) != "" {
		startOpts = append(startOpts, oteltrace.WithAttributes(attribute.String(attrExperimentDesc, description)))
	}
	ctx, span := atrace.Tracer.Start(ctx, "experiment-item-task", startOpts...)
	if !span.IsRecording() {
		span.End()
		return nil, nil, nil, errors.New("langfuse v4 experiments need the experiment item span to be recorded: " +
			"register a tracer provider with an exporter, and use a sampler that records every root span " +
			"(for example sdktrace.AlwaysSample); a ratio sampler drops some cases")
	}
	itemAttrs := []attribute.KeyValue{attribute.String(attrExperimentRootID, span.SpanContext().SpanID().String())}
	metadata, err := stringifyValue("experiment item metadata", spec.TraceMetadata["datasetItemMetadata"])
	if err != nil {
		span.End()
		return nil, nil, nil, err
	}
	if metadata != "" {
		itemAttrs = append(itemAttrs, attribute.String(attrExperimentItemMetadata, metadata))
	}
	attrs = append(attrs,
		attribute.String("langfuse.trace.name", spec.TraceName),
		attribute.String("langfuse.user.id", caseUserID(spec)),
		attribute.String("langfuse.environment", h.environment),
		attribute.String("langfuse.observation.input", attributeValue(attrs, attrTraceInput)))
	attrs = append(attrs, itemAttrs...)
	span.SetAttributes(attrs...)
	bag := baggage.FromContext(ctx)
	for _, attr := range attrs {
		key := string(attr.Key)
		if !strings.HasPrefix(key, "langfuse.experiment.") &&
			!strings.HasPrefix(key, attrTraceMetadataPrefix) &&
			key != "langfuse.trace.name" && key != "langfuse.user.id" &&
			key != attrSessionID && key != attrTraceTags && key != "langfuse.environment" {
			continue
		}
		value := attr.Value.AsString()
		if attr.Value.Type() == attribute.STRINGSLICE {
			encoded, err := json.Marshal(attr.Value.AsStringSlice())
			if err != nil {
				span.End()
				return nil, nil, nil, fmt.Errorf("encode experiment tags: %w", err)
			}
			value = string(encoded)
		}
		member, err := baggage.NewMemberRaw(key, value)
		if err != nil {
			span.End()
			return nil, nil, nil, fmt.Errorf("encode experiment baggage: %w", err)
		}
		bag, err = bag.SetMember(member)
		if err != nil {
			span.End()
			return nil, nil, nil, fmt.Errorf("set experiment baggage: %w", err)
		}
	}
	ctx = baggage.ContextWithBaggage(ctx, bag)
	return context.WithValue(ctx, experimentSpanKey{}, span), span, itemAttrs, nil
}

func attributeValue(attrs []attribute.KeyValue, key string) string {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}

func finishExperimentItem(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
	span, ok := ctx.Value(experimentSpanKey{}).(oteltrace.Span)
	if !ok {
		return nil, nil
	}
	defer span.End()
	if args.Result != nil {
		inferences := args.Result.Inferences
		if len(inferences) > 0 && inferences[len(inferences)-1] != nil && inferences[len(inferences)-1].FinalResponse != nil {
			output := inferences[len(inferences)-1].FinalResponse.Content
			span.SetAttributes(attribute.String("langfuse.observation.output", output), attribute.String("langfuse.trace.output", output))
		}
		if args.Result.ErrorMessage != "" {
			span.SetStatus(codes.Error, args.Result.ErrorMessage)
		}
	}
	if args.Error != nil {
		span.RecordError(args.Error)
		span.SetStatus(codes.Error, args.Error.Error())
	}
	return nil, nil
}
