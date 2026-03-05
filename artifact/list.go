//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

// ListOptions configures List behavior (reserved for future).
type ListOptions struct{}

// ListOption configures List behavior (functional options style).
type ListOption func(*ListOptions)
