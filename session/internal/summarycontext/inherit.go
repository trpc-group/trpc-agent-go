//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summarycontext

import "context"

// InheritModelCallRecorder returns next with the current attempt's model-call
// recorder. A recorder already attached to next is replaced so a hook that
// returns a cached previous context cannot publish this attempt onto that
// earlier recorder. When current has no recorder, any recorder on next is
// masked with a typed-nil value so later RecordModelCall is a no-op.
func InheritModelCallRecorder(next, current context.Context) context.Context {
	return inheritPointerValue(next, current, modelCallKey{}, ModelCallFromContext)
}

// InheritTriggerRecorder returns next with the current attempt's trigger
// recorder, replacing or masking a recorder already attached to next under
// the same rules as InheritModelCallRecorder.
func InheritTriggerRecorder(next, current context.Context) context.Context {
	return inheritPointerValue(next, current, triggerKey{}, TriggerFromContext)
}

// InheritEventSelectionRecorder returns next with the current attempt's
// event-selection recorder, replacing or masking a recorder already attached
// to next under the same rules as InheritModelCallRecorder.
func InheritEventSelectionRecorder(next, current context.Context) context.Context {
	return inheritPointerValue(
		next,
		current,
		eventSelectionKey{},
		EventSelectionFromContext,
	)
}

func inheritPointerValue[T any](
	next, current context.Context,
	key any,
	lookup func(context.Context) *T,
) context.Context {
	if next == nil {
		next = context.Background()
	}
	recorder := lookup(current)
	if recorder == lookup(next) {
		return next
	}
	// recorder remains a typed pointer when nil, masking an older value.
	return context.WithValue(next, key, recorder)
}
