//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responseusage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAttachTimingForCallback_CreatesUsageAndRestores(t *testing.T) {
	timing := &model.TimingInfo{FirstTokenDuration: time.Millisecond}
	response := &model.Response{}

	attachment := AttachTimingForCallback(response, timing, nil)
	require.NotNil(t, response.Usage)
	require.Same(t, timing, response.Usage.TimingInfo)

	attachment.Restore()
	require.Nil(t, response.Usage)
}

func TestAttachTimingForCallback_RestoresExistingTimingInfo(t *testing.T) {
	oldTiming := &model.TimingInfo{FirstTokenDuration: time.Millisecond}
	newTiming := &model.TimingInfo{FirstTokenDuration: 2 * time.Millisecond}
	usage := &model.Usage{PromptTokens: 1, TimingInfo: oldTiming}
	response := &model.Response{Usage: usage}

	attachment := AttachTimingForCallback(response, newTiming, nil)
	require.Same(t, usage, response.Usage)
	require.Same(t, newTiming, response.Usage.TimingInfo)

	attachment.Restore()
	require.Same(t, usage, response.Usage)
	require.Same(t, oldTiming, response.Usage.TimingInfo)
}

func TestTimingAttachment_RestoreIfTimingInfoChanged(t *testing.T) {
	timing := &model.TimingInfo{FirstTokenDuration: time.Millisecond}
	updatedTiming := &model.TimingInfo{FirstTokenDuration: 2 * time.Millisecond}
	response := &model.Response{}

	attachment := AttachTimingForCallback(response, timing, nil)
	attachment.RestoreIfTimingInfoChanged(timing)
	require.NotNil(t, response.Usage)
	require.Same(t, timing, response.Usage.TimingInfo)

	attachment.RestoreIfTimingInfoChanged(updatedTiming)
	require.Nil(t, response.Usage)
}

func TestTimingAttachment_RestoreDetachesReusedPartialUsage(t *testing.T) {
	timing := &model.TimingInfo{FirstTokenDuration: time.Millisecond}
	var state PartialState
	first := &model.Response{IsPartial: true}
	second := &model.Response{IsPartial: true}

	AttachTiming(first, timing, &state)
	first.Usage.PromptTokens = 10
	attachment := AttachTimingForCallback(second, timing, &state)
	require.Same(t, first.Usage, second.Usage)

	attachment.RestoreIfTimingInfoChanged(nil)

	require.Nil(t, second.Usage)
	require.NotNil(t, first.Usage)
	require.Equal(t, 10, first.Usage.PromptTokens)
	require.Same(t, timing, first.Usage.TimingInfo)
}

func TestTimingAttachment_RestoreKeepsCallbackMutatedUsage(t *testing.T) {
	timing := &model.TimingInfo{FirstTokenDuration: time.Millisecond}
	response := &model.Response{}

	attachment := AttachTimingForCallback(response, timing, nil)
	response.Usage.PromptTokensDetails.CachedTokens = 10
	attachment.Restore()

	require.NotNil(t, response.Usage)
	require.Equal(t, 10, response.Usage.PromptTokensDetails.CachedTokens)
	require.Nil(t, response.Usage.TimingInfo)
}

func TestTimingAttachment_RestoreIgnoresReplacedUsage(t *testing.T) {
	timing := &model.TimingInfo{FirstTokenDuration: time.Millisecond}
	response := &model.Response{}

	attachment := AttachTimingForCallback(response, timing, nil)
	replacedUsage := &model.Usage{PromptTokens: 10}
	response.Usage = replacedUsage
	attachment.Restore()

	require.Same(t, replacedUsage, response.Usage)
	require.Nil(t, response.Usage.TimingInfo)
}

func TestAttachTiming_ReusesPartialUsageForSameTimingInfo(t *testing.T) {
	timing := &model.TimingInfo{FirstTokenDuration: time.Millisecond}
	var state PartialState
	first := &model.Response{IsPartial: true}
	second := &model.Response{IsPartial: true}

	AttachTiming(first, timing, &state)
	AttachTiming(second, timing, &state)

	require.NotNil(t, first.Usage)
	require.Same(t, first.Usage, second.Usage)
	require.Same(t, timing, first.Usage.TimingInfo)
}

func TestAttachTiming_UsesNewPartialUsageForDifferentTimingInfo(t *testing.T) {
	firstTiming := &model.TimingInfo{FirstTokenDuration: time.Millisecond}
	secondTiming := &model.TimingInfo{FirstTokenDuration: 2 * time.Millisecond}
	var state PartialState
	first := &model.Response{IsPartial: true}
	second := &model.Response{IsPartial: true}

	AttachTiming(first, firstTiming, &state)
	AttachTiming(second, secondTiming, &state)

	require.NotNil(t, first.Usage)
	require.NotNil(t, second.Usage)
	require.NotSame(t, first.Usage, second.Usage)
	require.Same(t, firstTiming, first.Usage.TimingInfo)
	require.Same(t, secondTiming, second.Usage.TimingInfo)
}

func TestAttachTiming_NoopsOnNilResponseOrTimingInfo(t *testing.T) {
	AttachTiming(nil, &model.TimingInfo{}, nil)

	response := &model.Response{}
	AttachTiming(response, nil, nil)
	require.Nil(t, response.Usage)
}
