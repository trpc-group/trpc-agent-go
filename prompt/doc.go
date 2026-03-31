//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package prompt provides minimal prompt template helpers.
//
// The package intentionally starts small:
//   - text templates with {name} placeholders
//   - explicit variable rendering
//   - lightweight prompt identity metadata
//
// More advanced behaviors such as session-state injection, few-shot assembly,
// chat prompt composition, and remote prompt registries remain in their
// existing packages for now.
package prompt
