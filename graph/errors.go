//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import "errors"

// Errors.
var (
	ErrLineageIDRequired                = errors.New("lineage_id is required")
	ErrLineageIDEmpty                   = errors.New("lineage_id cannot be empty")
	ErrLineageIDAndCheckpointIDRequired = errors.New("lineage_id and checkpoint_id are required")
	ErrCheckpointNotFound               = errors.New("checkpoint not found")
	ErrDagEngineCheckpointUnsupported   = errors.New(
		"dag engine does not support checkpoints yet",
	)
	ErrDagEngineInterruptUnsupported = errors.New(
		"dag engine does not support interrupts yet",
	)
)
