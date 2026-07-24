//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package toolresultfile provides an opt-in plugin that externalizes large
// model-facing tool results to artifact storage.
//
// The replacement message contains a pinned artifact:// reference. Agents that
// have the file tool can inspect the result incrementally with read_file,
// keeping the original payload out of subsequent model requests and session
// events.
package toolresultfile
