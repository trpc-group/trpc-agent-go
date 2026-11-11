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
type tokenTailoringDisabledKey struct{}

// WithTokenTailoringDisabled exempts only request from token tailoring. Other
// requests using this context, including auxiliary callback calls, are not
// exempt. Delegating the approved request requires passing the same pointer.
func WithTokenTailoringDisabled(ctx context.Context, request *model.Request) context.Context {
	return context.WithValue(ctx, tokenTailoringDisabledKey{}, request)
}

// TokenTailoringDisabled reports whether ctx approves this exact request.
func TokenTailoringDisabled(ctx context.Context, request *model.Request) bool {
	if ctx == nil || request == nil {
		return false
	}
	approved, _ := ctx.Value(tokenTailoringDisabledKey{}).(*model.Request)
	return approved == request
}

// TokenTailoringRecord describes one provider-side request transformation.
type TokenTailoringRecord struct {
	Provider       string
	MaxInputTokens int
	BeforeMessages int
	AfterMessages  int
}

// TokenTailoringObserver collects provider-side request transformations.
type TokenTailoringObserver struct {
	mu       sync.Mutex
	records  []TokenTailoringRecord
	onRecord func(TokenTailoringRecord)
}

// ObserveTokenTailoring attaches a new observer to ctx. onRecord is called
// after each record has been stored and may be nil.
func ObserveTokenTailoring(
	ctx context.Context,
	onRecord func(TokenTailoringRecord),
) (context.Context, *TokenTailoringObserver) {
	if ctx == nil {
		ctx = context.Background()
	}
	observer := &TokenTailoringObserver{onRecord: onRecord}
	return context.WithValue(ctx, tokenTailoringObserverKey{}, observer), observer
}

// RecordTokenTailoring records a provider-side request transformation when an
// observer is attached to ctx.
func RecordTokenTailoring(ctx context.Context, record TokenTailoringRecord) {
	if ctx == nil {
		return
	}
	observer, ok := ctx.Value(tokenTailoringObserverKey{}).(*TokenTailoringObserver)
	if !ok || observer == nil {
		return
	}
	observer.mu.Lock()
	observer.records = append(observer.records, record)
	onRecord := observer.onRecord
	observer.mu.Unlock()
	if onRecord != nil {
		onRecord(record)
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
