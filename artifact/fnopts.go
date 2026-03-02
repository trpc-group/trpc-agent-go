//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

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

// applyPutOptions applies functional options onto a PutOptions value.
func applyPutOptions(opts ...PutOption) PutOptions {
	var o PutOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

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

// applyListOptions applies functional options onto a ListOptions value.
func applyListOptions(opts ...ListOption) ListOptions {
	var o ListOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// DeleteOption configures Delete behavior (functional options style).
type DeleteOption func(*DeleteOptions)

// DeleteAllOpt sets Mode=DeleteAll (default).
func DeleteAllOpt() DeleteOption {
	return func(o *DeleteOptions) {
		if o == nil {
			return
		}
		o.Mode = DeleteAll
		o.Version = ""
	}
}

// DeleteLatestOpt sets Mode=DeleteLatest.
func DeleteLatestOpt() DeleteOption {
	return func(o *DeleteOptions) {
		if o == nil {
			return
		}
		o.Mode = DeleteLatest
		o.Version = ""
	}
}

// DeleteVersionOpt sets Mode=DeleteVersion and the target version.
func DeleteVersionOpt(ver VersionID) DeleteOption {
	return func(o *DeleteOptions) {
		if o == nil {
			return
		}
		o.Mode = DeleteVersion
		o.Version = ver
	}
}

// applyDeleteOptions applies functional options onto a DeleteOptions value,
// and returns it.
func applyDeleteOptions(opts ...DeleteOption) DeleteOptions {
	var o DeleteOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

