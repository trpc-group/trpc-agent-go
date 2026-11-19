//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

// ToolResultContent is an ordered sequence of content parts returned by a tool.
//
// Return this type by value from a tool to opt into the framework's structured
// tool-result conversion. The default LLMAgent and Graph tool flows can convert
// it to a RoleTool message. In LLMAgent, an explicitly configured result formatter
// takes precedence and produces text; ToolResultMessages can then override the
// message. Graph ToolsNode uses the default structured-content conversion.
//
// Text-only values without content references are joined without separators into
// Message.Content. When a value contains a non-text part or a content reference,
// its parts are retained in order in
// Message.ContentParts and are not duplicated in Message.Content. Empty and nil
// values, including text-only values whose joined text is empty, produce a
// RoleTool message with an explicit empty text part. This keeps a completed
// empty result distinct from a payload-free placeholder. Conversion does not mutate this
// value and copies its parts, including mutable media data and content references,
// into the resulting message. Callers must not mutate the returned parts or
// backing buffers while the result is being processed.
//
// The type can carry image, audio, video, file, and unknown content parts, but
// each model adapter must support the supplied parts for them to be usable.
// Provider-specific validation belongs to the adapter. Content bytes and
// ContentRef values are passed through without network access, hydration, or
// encoding changes. Chat Completions does not automatically inject this result
// as a separate user message.
type ToolResultContent []ContentPart
