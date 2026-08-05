//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type summarySourceTooLargeError struct {
	sourceTokens int
	budget       int
}

func (e *summarySourceTooLargeError) Error() string {
	return fmt.Sprintf(
		"summary source conversation requires %d tokens but input budget is %d; refusing to omit unsummarized conversation",
		e.sourceTokens,
		e.budget,
	)
}

func isSummarySourceTooLarge(err error) bool {
	var target *summarySourceTooLargeError
	return errors.As(err, &target)
}

type summarySource struct {
	input            summaryPromptInput
	boundaryEvents   []event.Event
	boundary         summaryview.Boundary
	hasBoundary      bool
	prefixEvents     []event.Event
	prefixTexts      []string
	prefixBoundaries []summaryview.Boundary
	allowPrefix      bool
}

func (s *sessionSummarizer) buildSafeSummaryPrefixRequest(
	ctx context.Context,
	source *summarySource,
	budget int,
) (*model.Request, bool, error) {
	if source == nil || len(source.prefixEvents) == 0 {
		return nil, false, nil
	}
	ends, err := safeSummaryPrefixEndsForSource(source)
	if err != nil {
		return nil, false, err
	}
	if len(ends) == 0 {
		return nil, false, nil
	}

	buildCandidate := func(end int) (
		*model.Request,
		summaryPromptInput,
		bool,
		error,
	) {
		input := summaryPromptInput{
			conversationText: joinSummaryEventTexts(source.prefixTexts[:end]),
			previousSummary:  source.input.previousSummary,
		}
		request, err := s.buildBoundedStandaloneSummaryRequest(
			ctx,
			input,
			budget,
		)
		if isSummarySourceTooLarge(err) {
			return nil, input, false, nil
		}
		if err != nil {
			return nil, input, false, err
		}
		return request, input, true, nil
	}

	// Verify the smallest complete prefix first. This guarantees that the
	// fallback either makes structural progress or keeps the original
	// fail-closed behavior, independently of tokenization at later boundaries.
	request, input, fits, err := buildCandidate(ends[0])
	if err != nil || !fits {
		return nil, false, err
	}
	bestRequest, bestInput, bestEnd := request, input, ends[0]

	// Conversation prefixes grow monotonically in source content. Use bounded
	// search to minimize repeated tokenization of large histories, validating
	// every candidate against the fully rendered standalone request.
	low, high := 1, len(ends)-1
	for low <= high {
		mid := low + (high-low)/2
		request, input, fits, err = buildCandidate(ends[mid])
		if err != nil {
			return nil, false, err
		}
		if fits {
			bestRequest, bestInput, bestEnd = request, input, ends[mid]
			low = mid + 1
			continue
		}
		high = mid - 1
	}

	source.input = bestInput
	source.prefixEvents = source.prefixEvents[:bestEnd]
	source.prefixTexts = source.prefixTexts[:bestEnd]
	if source.prefixBoundaries != nil {
		source.prefixBoundaries = source.prefixBoundaries[:bestEnd]
		source.boundary = source.prefixBoundaries[bestEnd-1]
		source.hasBoundary = true
	} else {
		source.boundaryEvents = source.prefixEvents
		source.hasBoundary = false
	}
	return bestRequest, true, nil
}

func safeSummaryPrefixEndsForSource(source *summarySource) ([]int, error) {
	if len(source.prefixTexts) != len(source.prefixEvents) {
		return nil, errors.New("summary prefix text does not match events")
	}
	if source.prefixBoundaries != nil &&
		len(source.prefixBoundaries) != len(source.prefixEvents) {
		return nil, errors.New("summary prefix boundary does not match events")
	}
	ends := safeSummaryPrefixEnds(source.prefixEvents)
	if source.prefixBoundaries != nil {
		mappedEnds := ends[:0]
		for _, end := range ends {
			if !source.prefixBoundaries[end-1].IsZero() {
				mappedEnds = append(mappedEnds, end)
			}
		}
		ends = mappedEnds
	}
	// A fallback must consume fewer events than the request that just failed.
	// Exclude the full source even if it is itself a structurally safe boundary,
	// so callers can treat selected=true as a strict progress guarantee.
	for len(ends) > 0 && ends[len(ends)-1] >= len(source.prefixEvents) {
		ends = ends[:len(ends)-1]
	}
	return summaryPrefixEndsWithNewText(ends, source.prefixTexts), nil
}

func summaryPrefixEndsWithNewText(ends []int, texts []string) []int {
	textIndex := 0
	out := ends[:0]
	for _, end := range ends {
		for textIndex < end && texts[textIndex] == "" {
			textIndex++
		}
		if textIndex >= end {
			continue
		}
		out = append(out, end)
		textIndex = end
	}
	return out
}

func (s *sessionSummarizer) extractConversationEventTexts(
	events []event.Event,
) []string {
	texts := make([]string, len(events))
	for i := range events {
		texts[i] = s.extractConversationText(events[i : i+1])
	}
	return texts
}

func joinSummaryEventTexts(texts []string) string {
	nonEmpty := make([]string, 0, len(texts))
	for _, text := range texts {
		if text != "" {
			nonEmpty = append(nonEmpty, text)
		}
	}
	return strings.Join(nonEmpty, "\n")
}

// safeSummaryPrefixEnds returns exclusive event indexes where the raw tail
// can begin without splitting a response stream or a tool call/result round.
func safeSummaryPrefixEnds(events []event.Event) []int {
	pendingToolCalls := make(map[string]struct{})
	seenToolCalls := make(map[string]struct{})
	hasUnmatchableToolCall := false
	ends := make([]int, 0, len(events))
	for i := range events {
		evt := events[i]
		for _, id := range summaryToolCallIDs(evt.Response) {
			if id == "" {
				hasUnmatchableToolCall = true
				continue
			}
			if _, ok := seenToolCalls[id]; ok {
				// Reused IDs make a later result ambiguous: it may complete
				// either call. Keep the remainder raw instead of guessing.
				hasUnmatchableToolCall = true
				continue
			}
			seenToolCalls[id] = struct{}{}
			pendingToolCalls[id] = struct{}{}
		}
		for _, id := range summaryToolResultIDs(evt.Response) {
			delete(pendingToolCalls, id)
		}

		if hasUnmatchableToolCall || len(pendingToolCalls) != 0 ||
			!canEndSummaryPrefix(events, i) {
			continue
		}
		ends = append(ends, i+1)
	}
	return ends
}

func canEndSummaryPrefix(events []event.Event, index int) bool {
	evt := events[index]
	if evt.ID == "" || evt.Timestamp.IsZero() || evt.Response == nil ||
		!evt.Response.IsValidContent() || evt.Response.IsPartial ||
		evt.Response.IsUserMessage() ||
		summaryResponseHasRole(evt.Response, model.RoleSystem) ||
		evt.Author == authorUser || evt.Author == authorSystem ||
		len(summaryToolCallIDs(evt.Response)) != 0 {
		return false
	}
	if index+1 >= len(events) || evt.Response.ID == "" ||
		events[index+1].Response == nil {
		return true
	}
	return events[index+1].Response.ID != evt.Response.ID
}

func summaryResponseHasRole(response *model.Response, role model.Role) bool {
	if response == nil {
		return false
	}
	for _, choice := range response.Choices {
		if choice.Message.Role == role || choice.Delta.Role == role {
			return true
		}
	}
	return false
}

func summaryToolCallIDs(response *model.Response) []string {
	if response == nil {
		return nil
	}
	var ids []string
	for _, choice := range response.Choices {
		for _, call := range choice.Message.ToolCalls {
			ids = append(ids, call.ID)
		}
		for _, call := range choice.Delta.ToolCalls {
			ids = append(ids, call.ID)
		}
	}
	return ids
}

func summaryToolResultIDs(response *model.Response) []string {
	if response == nil {
		return nil
	}
	var ids []string
	for _, choice := range response.Choices {
		for _, message := range []model.Message{
			choice.Message,
			choice.Delta,
		} {
			if message.ToolID != "" {
				ids = append(ids, message.ToolID)
			}
		}
	}
	return ids
}
