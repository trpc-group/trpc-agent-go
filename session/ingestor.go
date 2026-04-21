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

// IngestOptions captures the per-request settings for Ingestor.IngestSession.
//
// New fields can be added here over time without changing the Ingestor
// interface signature, so implementations stay forward compatible as
// ingestion semantics evolve. Callers configure it via the With* helpers
// (e.g. WithIngestMetadata) and implementations resolve the effective values
// by applying each option to a local IngestOptions value in a for-loop,
// mirroring the SummaryOption pattern in this package:
//
//	var req session.IngestOptions
//	for _, opt := range opts {
//	    if opt != nil {
//	        opt(&req)
//	    }
//	}
type IngestOptions struct {
	// Metadata is extra metadata to attach to the resulting memories.
	// Backends typically merge it into the per-record metadata payload.
	Metadata map[string]any
	// AgentID identifies the agent that produced the session content.
	// Backends that support multi-agent partitioning use it to scope memories.
	AgentID string
	// RunID identifies the conversation/run associated with the ingestion.
	// Backends that support per-run grouping use it to link related memories.
	RunID string
}

// IngestOption configures a single ingestion request. Use the With* helpers
// (e.g. WithIngestMetadata, WithIngestAgentID, WithIngestRunID) to set the
// well-known per-request fields, or construct a custom IngestOption that
// mutates IngestOptions directly.
type IngestOption func(*IngestOptions)

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

// Ingestor ingests a completed session transcript into an external long-term
// memory platform (e.g. mem0). Implementations enqueue the session for
// asynchronous ingestion; the runner calls IngestSession after each turn
// completes.
//
// The variadic IngestOption slice lets callers configure per-request
// behaviour (metadata, agent_id, run_id, ...) without breaking the interface
// as ingestion semantics evolve. Implementations resolve the effective values
// by applying each option to a local IngestOptions value (see the IngestOptions
// doc comment for the canonical snippet).
type Ingestor interface {
	IngestSession(ctx context.Context, sess *Session, opts ...IngestOption) error
}
