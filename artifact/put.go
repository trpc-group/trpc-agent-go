//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

// PutOptions configures Put behavior.
type PutOptions struct {
	MimeType string
}

// PutOption configures Put behavior (functional options style).
type PutOption func(*PutOptions)

// WithPutMimeType sets PutOptions.MimeType.
func WithPutMimeType(mimeType string) PutOption {
	return func(o *PutOptions) {
		if o == nil {
			return
		}
		o.MimeType = mimeType
	}
}

