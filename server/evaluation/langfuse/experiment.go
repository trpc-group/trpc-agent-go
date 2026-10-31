//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"
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
	attrTraceInput          = "langfuse.trace.input"
	attrTraceTags           = "langfuse.trace.tags"
	attrTraceMetadataPrefix = "langfuse.trace.metadata."
	attrSessionID           = "langfuse.session.id"
	attrExperimentID        = "langfuse.experiment.id"
	attrExperimentName      = "langfuse.experiment.name"
	attrExperimentDesc      = "langfuse.experiment.description"
	attrExperimentMetadata  = "langfuse.experiment.metadata"
	attrExperimentDataset   = "langfuse.experiment.dataset.id"
	attrExperimentItemID    = "langfuse.experiment.item.id"
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
	payload = strings.NewReplacer("\\u2028", "\u2028", "\\u2029", "\u2029").Replace(payload)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])[:16], nil
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
// run item used to create. They are set on the case's root agent span, so they
// must be known before the case runs; the trace output is the root observation's
// own output in v4 and is no longer written separately.
func (h *Handler) v4CaseAttributes(spec *CaseSpec, opts executionOptions, sessionID string) ([]attribute.KeyValue, error) {
	attrs := []attribute.KeyValue{
		attribute.String(attrExperimentID, opts.experimentID),
		attribute.String(attrExperimentName, opts.runName),
		attribute.String(attrExperimentDataset, opts.datasetID),
		attribute.String(attrExperimentItemID, spec.DatasetItemID),
	}
	if strings.TrimSpace(opts.runDescription) != "" {
		attrs = append(attrs, attribute.String(attrExperimentDesc, opts.runDescription))
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
