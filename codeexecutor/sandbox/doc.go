//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sandbox provides an OS-level sandbox code executor. Runtime supports
// both buffered RunProgram calls and full-duplex StartProcess calls; both use
// the same workspace, permission, environment, and native-backend enforcement.
package sandbox
