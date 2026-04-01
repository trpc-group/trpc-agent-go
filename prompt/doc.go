//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package prompt provides small text prompt rendering helpers.
//
// The package centers on one text template type with:
//   - explicit syntax selection for either {name} or {{name}} placeholders
//   - optional resolver-backed placeholders such as {user:name}
//   - lightweight prompt identity metadata
//
// Double-curly placeholders are treated as variable substitution only; this
// package does not implement full Mustache control syntax such as sections or
// partials.
//
// More advanced behaviors such as few-shot assembly, chat prompt composition,
// and remote prompt registries remain in their existing packages for now.
package prompt
