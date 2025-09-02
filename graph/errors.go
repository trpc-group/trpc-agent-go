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
	ErrThreadIDRequired                = errors.New("thread_id is required")
	ErrThreadIDEmpty                   = errors.New("thread_id cannot be empty")
	ErrThreadIDAndCheckpointIDRequired = errors.New("thread_id and checkpoint_id are required")
	ErrCheckpointNotFound              = errors.New("checkpoint not found")
)
