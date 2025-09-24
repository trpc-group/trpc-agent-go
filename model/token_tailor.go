//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"context"
	"fmt"
	"unicode/utf8"
)

// TokenCounter counts tokens for messages and tools.
// The implementation is model-agnostic to keep the model package lightweight.
type TokenCounter interface {
	// CountTokens returns the estimated token count for the provided messages.
	CountTokens(ctx context.Context, messages []Message) (int, error)

	// RemainingTokens returns remaining tokens allowed given current usage.
	// Implementations may use an internal maxTokens configuration to compute remaining.
	RemainingTokens(ctx context.Context, messages []Message) (int, error)
}

// TailoringStrategy tailors messages to fit within a token budget.
type TailoringStrategy interface {
	// TailorMessages reduces message list so total tokens are within maxTokens.
	TailorMessages(ctx context.Context, messages []Message, maxTokens int) ([]Message, error)
}

// SimpleTokenCounter provides a very rough token estimation based on rune length.
// Heuristic: approximately one token per four UTF-8 runes for text content fields.
type SimpleTokenCounter struct {
	maxTokens int
}

// NewSimpleTokenCounter creates a SimpleTokenCounter with a max token budget.
func NewSimpleTokenCounter(maxTokens int) *SimpleTokenCounter {
	return &SimpleTokenCounter{maxTokens: maxTokens}
}

// CountTokens estimates tokens for messages (tools currently ignored in estimate).
func (c *SimpleTokenCounter) CountTokens(_ context.Context, messages []Message) (int, error) {
	total := 0
	for _, msg := range messages {
		// Count main content.
		total += utf8.RuneCountInString(msg.Content) / 4

		// Count reasoning content if present.
		if msg.ReasoningContent != "" {
			total += utf8.RuneCountInString(msg.ReasoningContent) / 4
		}

		// Count text parts in multimodal content.
		for _, part := range msg.ContentParts {
			if part.Text != nil {
				total += utf8.RuneCountInString(*part.Text) / 4
			}
		}
	}
	if total < 0 {
		return 0, fmt.Errorf("invalid negative token count for %v", messages)
	}
	return total, nil
}

// RemainingTokens returns the remaining tokens based on the internal maxTokens.
func (c *SimpleTokenCounter) RemainingTokens(ctx context.Context, messages []Message) (int, error) {
	used, err := c.CountTokens(ctx, messages)
	if err != nil {
		return 0, err
	}
	return c.maxTokens - used, nil
}

// MiddleOutStrategy removes messages from the middle until within token budget.
// After trimming, if the first message is a tool result, it will be removed.
type MiddleOutStrategy struct {
	tokenCounter TokenCounter
}

// NewMiddleOutStrategy constructs a middle-out strategy with the given counter.
func NewMiddleOutStrategy(counter TokenCounter) *MiddleOutStrategy {
	return &MiddleOutStrategy{tokenCounter: counter}
}

// TailorMessages implements middle-out trimming.
func (s *MiddleOutStrategy) TailorMessages(ctx context.Context, messages []Message, maxTokens int) ([]Message, error) {
	tailored := make([]Message, len(messages))
	copy(tailored, messages)

	for {
		tokenCount, err := s.tokenCounter.CountTokens(ctx, tailored)
		if err != nil {
			return nil, fmt.Errorf("count tokens failed: %w", err)
		}
		// Stop when tokens are within the limit or when the message list is empty.
		if tokenCount <= maxTokens || len(tailored) == 0 {
			break
		}
		// Remove the middle message.
		middleIndex := len(tailored) / 2
		tailored = append(tailored[:middleIndex], tailored[middleIndex+1:]...)
	}

	// Remove the first function execution result message if present.
	if len(tailored) > 0 && tailored[0].Role == RoleTool {
		tailored = tailored[1:]
	}
	return tailored, nil
}

// HeadOutStrategy deletes messages from the head (oldest first) until within limit.
// Options allow preserving the initial system message and the last turn.
type HeadOutStrategy struct {
	tokenCounter          TokenCounter
	PreserveSystemMessage bool
	PreserveLastTurn      bool
}

// NewHeadOutStrategy constructs a head-out strategy with the given counter.
func NewHeadOutStrategy(counter TokenCounter, preserveSystem, preserveLastTurn bool) *HeadOutStrategy {
	return &HeadOutStrategy{
		tokenCounter:          counter,
		PreserveSystemMessage: preserveSystem,
		PreserveLastTurn:      preserveLastTurn,
	}
}

// TailorMessages removes from the head while respecting preservation options.
func (s *HeadOutStrategy) TailorMessages(ctx context.Context, messages []Message, maxTokens int) ([]Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	tailored := make([]Message, len(messages))
	copy(tailored, messages)

	// Compute protected tail count if preserving last turn
	protectedTail := 0
	if s.PreserveLastTurn && len(tailored) >= 1 {
		protectedTail = 1
		if len(tailored) >= 2 {
			protectedTail = 2
		}
	}

	head := 0
	for {
		if head > len(tailored)-protectedTail {
			break
		}
		tokens, err := s.tokenCounter.CountTokens(ctx, tailored[head:len(tailored)-protectedTail])
		if err != nil {
			return nil, fmt.Errorf("count tokens failed: %w", err)
		}
		if tokens <= maxTokens {
			break
		}
		if s.PreserveSystemMessage && head == 0 && len(tailored) > 0 && tailored[0].Role == RoleSystem {
			head++
			continue
		}
		head++
	}
	if head >= len(tailored)-protectedTail {
		return []Message{}, nil
	}
	return append([]Message{}, tailored[head:len(tailored)-protectedTail]...), nil
}

// TailOutStrategy deletes messages from the tail (newest first) until within limit.
// Options allow preserving the initial system message and the last turn.
type TailOutStrategy struct {
	tokenCounter          TokenCounter
	PreserveSystemMessage bool
	PreserveLastTurn      bool
}

// NewTailOutStrategy constructs a tail-out strategy with the given counter.
func NewTailOutStrategy(counter TokenCounter, preserveSystem, preserveLastTurn bool) *TailOutStrategy {
	return &TailOutStrategy{
		tokenCounter:          counter,
		PreserveSystemMessage: preserveSystem,
		PreserveLastTurn:      preserveLastTurn,
	}
}

// TailorMessages removes from the tail while respecting preservation options.
func (s *TailOutStrategy) TailorMessages(ctx context.Context, messages []Message, maxTokens int) ([]Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	// Clone to avoid mutating caller slice.
	tailored := make([]Message, len(messages))
	copy(tailored, messages)

	// 1) Determine preserved segments.
	protectHead := 0
	if s.PreserveSystemMessage && tailored[0].Role == RoleSystem {
		protectHead = 1
	}

	tailPreserve := 0
	if s.PreserveLastTurn {
		if len(tailored) >= 2 {
			tailPreserve = 2
		} else {
			tailPreserve = 1
		}
	}

	// 2) Precompute fixed token costs for preserved segments.
	sysTokens := 0
	if protectHead > 0 {
		t, err := s.tokenCounter.CountTokens(ctx, tailored[:protectHead])
		if err != nil {
			return nil, fmt.Errorf("count tokens failed: %w", err)
		}
		sysTokens = t
	}
	tailTokens := 0
	if tailPreserve > 0 {
		t, err := s.tokenCounter.CountTokens(ctx, tailored[len(tailored)-tailPreserve:])
		if err != nil {
			return nil, fmt.Errorf("count tokens failed: %w", err)
		}
		tailTokens = t
	}

	// 3) Define the mutable middle window and trim from its end until budget fits.
	middleStart := protectHead
	middleEnd := len(tailored) - tailPreserve

	// If nothing to include besides preserved segments, just validate budget.
	if middleEnd <= middleStart {
		if sysTokens+tailTokens <= maxTokens {
			return append(append([]Message{}, tailored[:protectHead]...), tailored[len(tailored)-tailPreserve:]...), nil
		}
		return []Message{}, nil
	}

	for middleEnd > middleStart {
		midTokens, err := s.tokenCounter.CountTokens(ctx, tailored[middleStart:middleEnd])
		if err != nil {
			return nil, fmt.Errorf("count tokens failed: %w", err)
		}
		if sysTokens+midTokens+tailTokens <= maxTokens {
			break
		}
		middleEnd-- // remove one from the end of the middle window
	}

	// 4) Build final result: head + middle + tail (all optional based on flags).
	res := make([]Message, 0, protectHead+(middleEnd-middleStart)+tailPreserve)
	if protectHead > 0 {
		res = append(res, tailored[:protectHead]...)
	}
	if middleEnd > middleStart {
		res = append(res, tailored[middleStart:middleEnd]...)
	}
	if tailPreserve > 0 {
		res = append(res, tailored[len(tailored)-tailPreserve:]...)
	}
	// If still over budget (extreme case), return empty.
	if total, err := s.tokenCounter.CountTokens(ctx, res); err != nil {
		return nil, fmt.Errorf("count tokens failed: %w", err)
	} else if total > maxTokens {
		return []Message{}, nil
	}
	return res, nil
}
