//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package modelrequest

import (
	"context"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type tokenTailoringObserverKey struct{}

// TokenTailoringRecord describes one provider-side request transformation.
type TokenTailoringRecord struct {
	Provider       string
	MaxInputTokens int
	BeforeMessages int
	AfterMessages  int
	Provenance     TokenTailoringProvenance
}

// TokenTailoringProvenance describes whether a provider-side transformation
// preserves the identity and order of the input messages.
type TokenTailoringProvenance uint8

const (
	// TokenTailoringProvenanceUnknown means the transformation has no complete
	// source mapping and must be treated as unsafe for derived request state.
	TokenTailoringProvenanceUnknown TokenTailoringProvenance = iota
	// TokenTailoringProvenancePreserved means every output message is proven to
	// correspond one-to-one with the input message at the same index.
	TokenTailoringProvenancePreserved
	// TokenTailoringProvenanceDropped means at least one input message was
	// removed from the provider request.
	TokenTailoringProvenanceDropped
)

// String returns the stable diagnostic name for the provenance value.
func (p TokenTailoringProvenance) String() string {
	switch p {
	case TokenTailoringProvenancePreserved:
		return "preserved"
	case TokenTailoringProvenanceDropped:
		return "dropped"
	default:
		return "unknown"
	}
}

// TokenTailoringChange contains the request snapshots needed to update
// request-derived state after a proven-safe transformation. The snapshots are
// delivered synchronously to the observer and are not retained in diagnostics.
type TokenTailoringChange struct {
	Record TokenTailoringRecord
	Before []model.Message
	After  []model.Message
}

// TokenTailoringObserver collects provider-side request transformations.
type TokenTailoringObserver struct {
	mu       sync.Mutex
	records  []TokenTailoringRecord
	onChange func(TokenTailoringChange)
}

// ObserveTokenTailoring attaches a new observer to ctx. onChange is called
// synchronously after each diagnostic record has been stored and may be nil.
func ObserveTokenTailoring(
	ctx context.Context,
	onChange func(TokenTailoringChange),
) (context.Context, *TokenTailoringObserver) {
	if ctx == nil {
		ctx = context.Background()
	}
	observer := &TokenTailoringObserver{onChange: onChange}
	return context.WithValue(ctx, tokenTailoringObserverKey{}, observer), observer
}

// RecordTokenTailoring records a provider-side request transformation when an
// observer is attached to ctx.
func RecordTokenTailoring(ctx context.Context, record TokenTailoringRecord) {
	RecordTokenTailoringChange(ctx, TokenTailoringChange{Record: record})
}

// RecordTokenTailoringChange records a provider-side request transformation
// and synchronously exposes its request snapshots to the attached observer.
func RecordTokenTailoringChange(ctx context.Context, change TokenTailoringChange) {
	if ctx == nil {
		return
	}
	observer, ok := ctx.Value(tokenTailoringObserverKey{}).(*TokenTailoringObserver)
	if !ok || observer == nil {
		return
	}
	observer.mu.Lock()
	observer.records = append(observer.records, change.Record)
	onChange := observer.onChange
	observer.mu.Unlock()
	if onChange != nil {
		onChange(change)
	}
}

// Snapshot returns an isolated copy of the records collected so far.
func (o *TokenTailoringObserver) Snapshot() []TokenTailoringRecord {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]TokenTailoringRecord(nil), o.records...)
}
