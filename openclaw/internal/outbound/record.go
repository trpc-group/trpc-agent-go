//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package outbound

import (
	"context"
	"sync"
)

type sentTextRecorderContextKey struct{}

// SentTextRecorder tracks successful text sends within one agent run.
type SentTextRecorder struct {
	mu      sync.Mutex
	sent    map[sentTextKey]struct{}
	targets map[sentTargetKey]struct{}
}

type sentTextKey struct {
	Channel string
	Target  string
	Text    string
}

type sentTargetKey struct {
	Channel string
	Target  string
}

// NewSentTextRecorder creates an empty per-run delivery recorder.
func NewSentTextRecorder() *SentTextRecorder {
	return &SentTextRecorder{
		sent:    make(map[sentTextKey]struct{}),
		targets: make(map[sentTargetKey]struct{}),
	}
}

// WithSentTextRecorder attaches a per-run recorder to a context.
func WithSentTextRecorder(
	ctx context.Context,
	recorder *SentTextRecorder,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, sentTextRecorderContextKey{}, recorder)
}

// Record stores a successful text delivery.
func (r *SentTextRecorder) Record(target DeliveryTarget, text string) {
	key, ok := sentTextKeyFor(target, text)
	if r == nil || !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sent == nil {
		r.sent = make(map[sentTextKey]struct{})
	}
	if r.targets == nil {
		r.targets = make(map[sentTargetKey]struct{})
	}
	r.sent[key] = struct{}{}
	r.targets[sentTargetKey{
		Channel: key.Channel,
		Target:  key.Target,
	}] = struct{}{}
}

// Contains reports whether the exact text target was already delivered.
func (r *SentTextRecorder) Contains(
	target DeliveryTarget,
	text string,
) bool {
	key, ok := sentTextKeyFor(target, text)
	if r == nil || !ok {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok = r.sent[key]
	return ok
}

// ContainsTarget reports whether any text was delivered to the target.
func (r *SentTextRecorder) ContainsTarget(target DeliveryTarget) bool {
	key, ok := sentTargetKeyFor(target)
	if r == nil || !ok {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok = r.targets[key]
	return ok
}

func sentTextRecorderFromContext(
	ctx context.Context,
) (*SentTextRecorder, bool) {
	if ctx == nil {
		return nil, false
	}
	recorder, ok := ctx.Value(
		sentTextRecorderContextKey{},
	).(*SentTextRecorder)
	return recorder, ok && recorder != nil
}

func sentTextKeyFor(
	target DeliveryTarget,
	text string,
) (sentTextKey, bool) {
	targetKey, ok := sentTargetKeyFor(target)
	if !ok || text == "" {
		return sentTextKey{}, false
	}
	return sentTextKey{
		Channel: targetKey.Channel,
		Target:  targetKey.Target,
		Text:    text,
	}, true
}

func sentTargetKeyFor(target DeliveryTarget) (sentTargetKey, bool) {
	clean := fillTargetFromOpaqueValue(sanitizeTarget(target))
	if clean.Channel == "" || clean.Target == "" {
		return sentTargetKey{}, false
	}
	return sentTargetKey{
		Channel: clean.Channel,
		Target:  clean.Target,
	}, true
}
