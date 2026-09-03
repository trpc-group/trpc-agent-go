//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summarycontext

import "context"

// TriggerObservation is the built-in summarizer's published gate decision for
// one summary attempt. Published distinguishes an empty observation that means
// "no eligible content" from a custom summarizer that never published a
// trigger. Leftover caller Report.Trigger values are never stored here.
type TriggerObservation struct {
	Published      bool
	Name           string
	Metric         string
	Value          int
	Threshold      int
	ContextWindow  int
	CheckCount     int
	ThresholdRatio float64
}

type triggerKey struct{}

// WithTriggerRecorder attaches a recorder that the built-in summarizer fills
// when it publishes a trigger decision. A nil recorder makes RecordTrigger a
// no-op.
func WithTriggerRecorder(
	ctx context.Context,
	obs *TriggerObservation,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if obs == nil {
		return ctx
	}
	return context.WithValue(ctx, triggerKey{}, obs)
}

// RecordTrigger publishes the trigger observation for the current attempt,
// replacing any observation recorded earlier in the same attempt. It is a
// no-op when no recorder is attached to ctx.
func RecordTrigger(ctx context.Context, obs TriggerObservation) {
	recorder := TriggerFromContext(ctx)
	if recorder == nil {
		return
	}
	obs.Published = true
	*recorder = obs
}

// TriggerFromContext returns the attempt-local trigger recorder, or nil when
// none is attached.
func TriggerFromContext(ctx context.Context) *TriggerObservation {
	if ctx == nil {
		return nil
	}
	recorder, ok := ctx.Value(triggerKey{}).(*TriggerObservation)
	if !ok || recorder == nil {
		return nil
	}
	return recorder
}
