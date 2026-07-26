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
// The replacement message contains a pinned artifact:// reference. The active
// model request must expose read_file, either directly or with a tool-set prefix
// such as file_read_file. Results larger than the default file-reader limit are
// stored as ordered, readable chunks plus a small JSON manifest. Multipart
// results are first encoded as a JSON envelope containing content and
// content_parts.
//
// If read_file is filtered out, has not yet been dynamically activated, or the
// invocation uses read-only artifact storage (for example, a candidate-selection
// attempt), the plugin leaves the original result inline.
package toolresultfile
