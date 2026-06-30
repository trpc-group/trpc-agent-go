//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package multimodal provides a session.Service wrapper that externalizes
// multimodal content to artifact storage before persistence and hydrates it on
// reads.
package multimodal

import (
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	internal "trpc.group/trpc-go/trpc-agent-go/internal/session/multimodal"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	// ErrArtifactServiceNil indicates multimodal governance needed an artifact
	// service but none was configured.
	ErrArtifactServiceNil = internal.ErrArtifactServiceNil
	// ErrInvalidArtifactRef indicates an internal content reference cannot be
	// resolved into a pinned artifact name and version.
	ErrInvalidArtifactRef = internal.ErrInvalidArtifactRef
)

// Config controls session multimodal governance.
type Config struct {
	// Enabled enables session multimodal externalization and default hydrate.
	Enabled bool
}

// Wrap wraps a session service with multimodal externalization and hydration.
//
// When cfg.Enabled is false, Wrap returns inner unchanged. When enabled,
// AppendEvent persists supported inline multimodal content through artifact
// storage and stores references in session events. Read APIs hydrate those
// references back into runtime messages.
func Wrap(
	inner session.Service,
	artifactService artifact.Service,
	cfg Config,
) session.Service {
	return internal.Wrap(
		inner,
		artifactService,
		internal.Config{Enabled: cfg.Enabled},
	)
}
