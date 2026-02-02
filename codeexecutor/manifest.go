//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

import "encoding/json"

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

type outputSpecSnake struct {
	Globs         []string `json:"globs"`
	MaxFiles      *int     `json:"max_files"`
	MaxFileBytes  *int64   `json:"max_file_bytes"`
	MaxTotalBytes *int64   `json:"max_total_bytes"`
	Save          *bool    `json:"save"`
	NameTemplate  *string  `json:"name_template"`
	Inline        *bool    `json:"inline"`
}

// UnmarshalJSON accepts both legacy Go-style keys (MaxFiles) and the
// recommended snake_case keys (max_files).
func (s *OutputSpec) UnmarshalJSON(data []byte) error {
	type outputSpecAlias OutputSpec
	var base outputSpecAlias
	if err := json.Unmarshal(data, &base); err != nil {
		return err
	}
	var snake outputSpecSnake
	if err := json.Unmarshal(data, &snake); err != nil {
		return err
	}
	*s = OutputSpec(base)
	if snake.Globs != nil {
		s.Globs = snake.Globs
	}
	if snake.MaxFiles != nil {
		s.MaxFiles = *snake.MaxFiles
	}
	if snake.MaxFileBytes != nil {
		s.MaxFileBytes = *snake.MaxFileBytes
	}
	if snake.MaxTotalBytes != nil {
		s.MaxTotalBytes = *snake.MaxTotalBytes
	}
	if snake.Save != nil {
		s.Save = *snake.Save
	}
	if snake.NameTemplate != nil {
		s.NameTemplate = *snake.NameTemplate
	}
	if snake.Inline != nil {
		s.Inline = *snake.Inline
	}
	return nil
}

// FileRef references a file collected from workspace.
type FileRef struct {
	Name      string
	MIMEType  string
	Content   string
	SavedAs   string
	Version   int
	SizeBytes int64
	Truncated bool
}

// OutputManifest is the structured result of CollectOutputs.
type OutputManifest struct {
	Files     []FileRef
	LimitsHit bool
}
