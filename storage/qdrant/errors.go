//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import "errors"

// Sentinel errors for the qdrant storage package.
var (
	ErrEmptyHost = errors.New("qdrant: host cannot be empty")
)
