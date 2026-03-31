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
//   - {name} placeholders resolved from explicit Vars
//   - optional resolver-backed placeholders such as {user:name}
//   - lightweight prompt identity metadata
//
// Legacy Mustache-style placeholders like {{name}} are normalized into the
// canonical single-brace form during rendering.
//
// More advanced behaviors such as few-shot assembly, chat prompt composition,
// and remote prompt registries remain in their existing packages for now.
package prompt
