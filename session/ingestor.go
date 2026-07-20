//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import "context"

// IngestOptions contains backend-neutral hints for one call to
// Ingestor.IngestSession. Provider-specific ingestion policy and request
// fields belong to the provider package rather than this shared contract.
//
// Implementations may ignore hints they do not support and should document
// which hints they honor.
type IngestOptions struct {
	// Metadata contains caller-supplied attributes associated with the
	// ingested session.
	Metadata map[string]any
	// AgentID identifies the agent associated with the ingested session.
	AgentID string
	// RunID identifies the run associated with the ingested session.
	RunID string
}

// IngestOption configures the backend-neutral hints for one ingestion request.
type IngestOption func(*IngestOptions)

// ResolveIngestOptions applies opts in order and returns the resolved hints for
// one ingestion request. Nil options are ignored.
func ResolveIngestOptions(opts ...IngestOption) IngestOptions {
	var resolved IngestOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&resolved)
		}
	}
	return resolved
}

// WithIngestMetadata attaches the supplied metadata to a single ingestion
// request. Repeated calls merge the maps; later values overwrite earlier
// values for duplicate keys. Empty maps are ignored.
func WithIngestMetadata(metadata map[string]any) IngestOption {
	return func(o *IngestOptions) {
		if len(metadata) == 0 {
			return
		}
		if o.Metadata == nil {
			o.Metadata = make(map[string]any, len(metadata))
		}
		for k, v := range metadata {
			o.Metadata[k] = v
		}
	}
}

// WithIngestAgentID labels the ingestion batch with an agent identifier.
// Backends that support per-agent partitioning use it to scope memories.
// Empty values are ignored.
func WithIngestAgentID(agentID string) IngestOption {
	return func(o *IngestOptions) {
		if agentID == "" {
			return
		}
		o.AgentID = agentID
	}
}

// WithIngestRunID labels the ingestion batch with a run identifier so the
// backend can group memories produced within the same conversation/run.
// Empty values are ignored.
func WithIngestRunID(runID string) IngestOption {
	return func(o *IngestOptions) {
		if runID == "" {
			return
		}
		o.RunID = runID
	}
}

// Ingestor ingests completed session content into long-term memory. The runner
// calls IngestSession after each turn completes. Implementations may process
// the session synchronously or enqueue it for later processing.
//
// A nil return means that no synchronous submission error occurred; it does not
// guarantee durable persistence unless the implementation documents stronger
// delivery semantics. Options carry common caller hints. Provider-specific
// behavior is configured by the implementation that owns it.
type Ingestor interface {
	IngestSession(ctx context.Context, sess *Session, opts ...IngestOption) error
}
