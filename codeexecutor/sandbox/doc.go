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
//
// Runtime.Explain reports a high-level status summary for operators: requested
// and resolved backend, filesystem sandbox type, network mode, and managed
// backend preflight readiness. On managed profiles it may run the same short
// backend probe used by execution and cache the result on the Runtime.
// PreflightReady means the core backend probe succeeded, not that every
// reported policy boundary is enforceable. It is not a full policy dump and
// does not describe path grants, environment inheritance, timeouts, or
// resource quotas.
package sandbox
