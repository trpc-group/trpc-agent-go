//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package wirenorm defines shared empty-content normalization rules used by
// both message validation and provider-side tailoring observation.
package wirenorm

// EmptyContentPlaceholder is the placeholder written into Message.Content when
// a message carries non-text payload but has an empty Content string.
const EmptyContentPlaceholder = " "

// NeedsPlaceholder reports whether a message requires EmptyContentPlaceholder
// normalization. Native provider records (hasProviderData=true) must retain
// their bound projection without placeholders.
func NeedsPlaceholder(
	content string,
	hasProviderData bool,
	hasContentParts bool,
	hasToolCalls bool,
	hasReasoningContent bool,
) bool {
	if content != "" || hasProviderData {
		return false
	}
	return hasContentParts || hasToolCalls || hasReasoningContent
}
