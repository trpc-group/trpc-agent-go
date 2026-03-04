//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

// ListOption configures List behavior (functional options style).
type ListOption func(*ListOptions)

// WithListLimit sets ListOptions.Limit.
func WithListLimit(limit int) ListOption {
	return func(o *ListOptions) {
		if o == nil {
			return
		}
		o.Limit = limit
	}
}

// WithListPageToken sets ListOptions.PageToken.
func WithListPageToken(token string) ListOption {
	return func(o *ListOptions) {
		if o == nil {
			return
		}
		o.PageToken = token
	}
}

