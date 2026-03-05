//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

// PutOptions configures Put behavior (reserved for future).
type PutOptions struct{}

// PutOption configures Put behavior (functional options style).
type PutOption func(*PutOptions)
