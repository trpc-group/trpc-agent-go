//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package artifact provides the definition and service for content artifacts.
package artifact

// Artifact defines a content artifact, such as an image, video, or document.
// Artifacts serve as a key mechanism for handling named, versioned binary data,
// which may be linked to a particular user session or persistently associated with a user across sessions.
type Artifact struct {
	ArtifactDescriptor
	// Data contains the raw bytes (required).
	Data []byte `json:"data,omitempty"`
}

// SessionInfo contains the session information for artifact operations.
type SessionInfo struct {
	// AppName is the name of the application
	AppName string
	// UserID is the ID of the user
	UserID string
	// SessionID is the ID of the session
	SessionID string
}
