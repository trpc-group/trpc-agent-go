//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package promptiter defines domain entities used across PromptIter workflow stages.
//
// The package owns the contracts shared by traces, surfaces, gradients, losses,
// profiles, and patches so that sampler, backwarder, optimizer, and engine
// components exchange a consistent signal representation.
package promptiter
