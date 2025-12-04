//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

// InputSpec declares a single input mapping into the workspace.
//
// From supports schemes:
//   - artifact://name[@version]
//   - host://abs/path
//   - workspace://rel/path
//   - skill://name/rel/path
//
// To is a workspace-relative destination (default: WORK_DIR/inputs/<name>).
// Mode hints the strategy: "link" (symlink/hardlink where possible) or
// "copy" (default fallback when link is not possible).
type InputSpec struct {
	From string
	To   string
	Mode string
	Pin  bool
}

// OutputSpec declares outputs to collect and optionally persist.
// Globs are workspace-relative patterns; implementations should
// support ** semantics.
type OutputSpec struct {
	Globs         []string
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
	Save          bool
	NameTemplate  string
	Inline        bool
}

// FileRef references a file collected from workspace.
type FileRef struct {
	Name     string
	MIMEType string
	Content  string
	SavedAs  string
	Version  int
}

// OutputManifest is the structured result of CollectOutputs.
type OutputManifest struct {
	Files     []FileRef
	LimitsHit bool
}
