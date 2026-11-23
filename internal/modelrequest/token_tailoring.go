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
	// correspond to an input message without dropping or rewriting history.
	TokenTailoringProvenancePreserved
	// TokenTailoringProvenanceReduced means the output contains fewer messages
	// than the input (len(after) < len(before)).
	TokenTailoringProvenanceReduced
)

// String returns the stable diagnostic name for the provenance value.
func (p TokenTailoringProvenance) String() string {
	switch p {
	case TokenTailoringProvenancePreserved:
		return "preserved"
	case TokenTailoringProvenanceReduced:
		return "reduced"
	default:
		return "unknown"
	}
}

// PreservedTransform holds the synchronous request snapshots needed to rebase
// request-derived summary state after a proven-safe transformation.
type PreservedTransform struct {
	Before []model.Message
	After  []model.Message
	// SourceIndexes maps each output message to its source index in Before.
	// A nil slice represents the identity mapping (output i came from Before[i]).
	SourceIndexes []int
}

// TokenTailoringChange contains the diagnostic record and, only when
// Record.Provenance == TokenTailoringProvenancePreserved, the synchronous
// transform snapshots needed to rebase summary state. Snapshots are not
// retained in observer diagnostics after the callback returns.
type TokenTailoringChange struct {
	Record    TokenTailoringRecord
	Preserved *PreservedTransform
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

// HasTokenTailoringObserver reports whether ctx has a token-tailoring observer
// attached. Callers can use this to avoid snapshotting request messages when
// nothing is observing provider-side transformations.
func HasTokenTailoringObserver(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	observer, ok := ctx.Value(tokenTailoringObserverKey{}).(*TokenTailoringObserver)
	return ok && observer != nil
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
