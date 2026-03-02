//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

// Descriptor describes a specific artifact version without requiring its bytes.
// It can be used to implement deferred and/or streaming downloads.
type Descriptor struct {
	Key Key `json:"key,omitempty"`

	// Version is the resolved artifact version.
	Version VersionID `json:"version,omitempty"`

	// MimeType is the IANA standard MIME type of the artifact.
	MimeType string `json:"mime_type,omitempty"`

	// Size is the artifact size in bytes if known, otherwise 0.
	Size int64 `json:"size,omitempty"`

	// URL is an optional URL where the artifact can be accessed (e.g. presigned).
	URL string `json:"url,omitempty"`
}
