//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

// DeleteOptions controls Delete behavior (reserved for future).
type DeleteOptions struct{}

// DeleteOption configures Delete behavior (functional options style).
type DeleteOption func(*DeleteOptions)
