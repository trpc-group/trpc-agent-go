//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package responseusage provides helpers for attaching response usage metadata.
package responseusage

import "trpc.group/trpc-go/trpc-agent-go/model"

// PartialState stores reusable usage state for partial responses.
type PartialState struct {
	usage      *model.Usage
	timingInfo *model.TimingInfo
}

// TimingAttachment records a temporary TimingInfo attachment.
type TimingAttachment struct {
	response           *model.Response
	usage              *model.Usage
	timingInfo         *model.TimingInfo
	attachedUsage      *model.Usage
	attachedTimingInfo *model.TimingInfo
	createdUsage       bool
}

// AttachTimingForCallback attaches TimingInfo before callbacks and returns a restorer.
func AttachTimingForCallback(
	response *model.Response,
	timingInfo *model.TimingInfo,
	partialState *PartialState,
) TimingAttachment {
	attachment := TimingAttachment{response: response}
	if response != nil {
		attachment.usage = response.Usage
		if response.Usage != nil {
			attachment.timingInfo = response.Usage.TimingInfo
		}
	}
	AttachTiming(response, timingInfo, partialState)
	if response != nil {
		attachment.attachedUsage = response.Usage
		if response.Usage != nil {
			attachment.attachedTimingInfo = response.Usage.TimingInfo
		}
		attachment.createdUsage = attachment.usage == nil && response.Usage != nil
	}
	return attachment
}

// RestoreIfTimingInfoChanged restores the temporary attachment if the target TimingInfo changed.
func (a TimingAttachment) RestoreIfTimingInfoChanged(timingInfo *model.TimingInfo) {
	if timingInfo != a.attachedTimingInfo {
		a.Restore()
	}
}

// Restore removes the temporary attachment if it is still unchanged.
func (a TimingAttachment) Restore() {
	if a.response == nil || a.response.Usage == nil {
		return
	}
	if a.createdUsage {
		if a.response.Usage != a.attachedUsage {
			return
		}
		if usageOnlyHasTimingInfo(a.response.Usage) {
			a.response.Usage = nil
			return
		}
		if a.response.Usage.TimingInfo == a.attachedTimingInfo {
			a.response.Usage.TimingInfo = nil
		}
		return
	}
	if a.response.Usage == a.usage &&
		a.response.Usage.TimingInfo == a.attachedTimingInfo {
		a.response.Usage.TimingInfo = a.timingInfo
	}
}

func usageOnlyHasTimingInfo(usage *model.Usage) bool {
	if usage == nil {
		return true
	}
	return usage.PromptTokens == 0 &&
		usage.CompletionTokens == 0 &&
		usage.TotalTokens == 0 &&
		usage.PromptTokensDetails == (model.PromptTokensDetails{}) &&
		usage.CompletionTokensDetails == (model.CompletionTokensDetails{})
}

// AttachTiming attaches TimingInfo to response usage.
func AttachTiming(
	response *model.Response,
	timingInfo *model.TimingInfo,
	partialState *PartialState,
) {
	if response == nil || timingInfo == nil {
		return
	}
	if response.Usage == nil {
		if response.IsPartial {
			if partialState == nil {
				response.Usage = &model.Usage{}
			} else {
				if partialState.usage == nil ||
					partialState.timingInfo != timingInfo {
					partialState.usage = &model.Usage{}
					partialState.timingInfo = timingInfo
				}
				response.Usage = partialState.usage
			}
		} else {
			response.Usage = &model.Usage{}
		}
	}
	response.Usage.TimingInfo = timingInfo
}
