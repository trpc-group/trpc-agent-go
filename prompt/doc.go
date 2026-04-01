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
//   - three syntax modes: mixed (default), single-brace only, or double-curly only
//   - optional resolver-backed placeholders such as {user:name}
//   - lightweight prompt identity metadata
//
// The default SyntaxMixedBrace mode recognizes both {name} and {{name}}
// placeholders in the same template. SyntaxSingleBrace and SyntaxDoubleBrace
// restrict recognition to one delimiter style.
//
// Double-curly placeholders are treated as variable substitution only; this
// package does not implement full Mustache control syntax such as sections or
// partials.
//
// More advanced behaviors such as few-shot assembly, chat prompt composition,
// and remote prompt registries remain in their existing packages for now.
package prompt
