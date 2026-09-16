//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package messageoriginkey defines the dependency-free invocation state key
// used to share session-message origin across framework layers.
package messageoriginkey

// Key identifies invocation-scoped session-message origin.
const Key = "__session_message_origins__"
