//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evaluation

import (
	"slices"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

// withAdditionalCallbacks defers merging until the service applies its options,
// so callbacks configured on an injected service remain available as defaults.
func withAdditionalCallbacks(c *service.Callbacks) service.Option {
	return func(o *service.Options) {
		if c == nil {
			return
		}
		previous := o.Callbacks
		if previous == nil {
			previous = &service.Callbacks{}
		}
		callbacks := &service.Callbacks{
			BeforeInferenceSet:  slices.Concat(previous.BeforeInferenceSet, c.BeforeInferenceSet),
			AfterInferenceSet:   slices.Concat(previous.AfterInferenceSet, c.AfterInferenceSet),
			BeforeInferenceCase: slices.Concat(previous.BeforeInferenceCase, c.BeforeInferenceCase),
			AfterInferenceCase:  slices.Concat(previous.AfterInferenceCase, c.AfterInferenceCase),
			BeforeEvaluateSet:   slices.Concat(previous.BeforeEvaluateSet, c.BeforeEvaluateSet),
			AfterEvaluateSet:    slices.Concat(previous.AfterEvaluateSet, c.AfterEvaluateSet),
			BeforeEvaluateCase:  slices.Concat(previous.BeforeEvaluateCase, c.BeforeEvaluateCase),
			AfterEvaluateCase:   slices.Concat(previous.AfterEvaluateCase, c.AfterEvaluateCase),
		}
		service.WithCallbacks(callbacks)(o)
	}
}
