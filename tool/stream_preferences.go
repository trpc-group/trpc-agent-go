//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

// InnerTextMode controls whether forwarded inner assistant text is visible
// in the parent flow when inner streaming is enabled.
//
// This only affects forwarded events. It does not change how a child result
// is aggregated into the final tool response for the parent model.
type InnerTextMode string

const (
	// InnerTextModeDefault preserves the default behavior.
	InnerTextModeDefault InnerTextMode = ""

	// InnerTextModeInclude forwards inner assistant text to the parent flow.
	InnerTextModeInclude InnerTextMode = "include"

	// InnerTextModeExclude suppresses forwarded inner assistant text while
	// still aggregating that text into the final tool response.
	InnerTextModeExclude InnerTextMode = "exclude"
)

// NormalizeInnerTextMode normalizes a possibly empty or unknown mode to a
// concrete runtime behavior.
func NormalizeInnerTextMode(mode InnerTextMode) InnerTextMode {
	switch mode {
	case InnerTextModeExclude:
		return InnerTextModeExclude
	case InnerTextModeDefault, InnerTextModeInclude:
		return InnerTextModeInclude
	default:
		return InnerTextModeInclude
	}
}
