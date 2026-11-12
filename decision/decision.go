//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package decision evaluates named questions against caller-supplied evidence.
// Callers choose the evidence and interpret answers in their own workflow.
package decision

import (
	"context"
	"encoding/json"
)

// Decider answers a batch of independent questions about the same input.
// Decide takes a non-nil context and honors its cancellation. It does not
// modify the request; callers must not mutate the request during the call.
// On success, the caller owns the response, including its maps and pointers.
// An error indicates failure of the call as a whole. A refusal or missing
// answer is reported per question when the backend can identify it.
type Decider interface {
	Decide(ctx context.Context, req Request) (*Response, error)
}

// Request pairs shared evidence with one or more questions. Question IDs are
// unique within a request. Answers are correlated by ID; question order is not
// a dependency between answers.
type Request struct {
	Input     Input
	Questions []Question
}

// Input contains either nonblank UTF-8 text or a UTF-8 JSON object or array of
// objects. Set exactly one field. The caller supplies the evidence to evaluate.
type Input struct {
	Text string
	JSON json.RawMessage
}

// Kind identifies the expected result of a question.
type Kind string

const (
	// Predicate asks for the probability that a proposition is true.
	Predicate Kind = "predicate"
	// Choice asks for one of the supplied option IDs.
	Choice Kind = "choice"
	// Score rates the evidence against the supplied ordered levels.
	Score Kind = "score"
)

// Question is a named instruction with a specific answer kind. ID and
// Instructions must be nonblank UTF-8. Options must be empty for Predicate
// and have at least two entries for Choice and Score. For Score, options are
// levels in order from lowest to highest.
type Question struct {
	ID           string
	Kind         Kind
	Instructions string
	Options      []Option
}

// Option is a choice or score level. ID is nonblank and unique within its
// question. Description optionally explains the meaning to the model.
type Option struct {
	ID          string
	Description string
}

// Response contains one answer per requested question, keyed by question ID.
// Model and RequestID identify the call as reported by the backend.
type Response struct {
	Model     string
	RequestID string
	Usage     Usage
	Answers   map[string]Answer
}

// Usage contains optional backend-reported counts and input truncation
// evidence. Nil fields mean the backend did not report them.
type Usage struct {
	InputTokens  *int64
	OutputTokens *int64
	TotalTokens  *int64
	Truncated    *bool
}

// Status tells the caller whether a question has a usable answer.
type Status string

const (
	// Answered means the backend supplied an answer of the requested kind.
	Answered Status = "answered"
	// Refused means the backend explicitly declined this question.
	Refused Status = "refused"
	// Unavailable means the backend did not supply an answer for this question.
	Unavailable Status = "unavailable"
)

// Answer contains one result of the requested kind when Status is Answered.
// Refused and Unavailable answers have no result fields. A zero probability or
// score is a valid value, distinct from an absent field.
type Answer struct {
	Status Status

	// Probability is P(true) for a Predicate question.
	Probability *float64
	// Choice is the selected option ID for a Choice question.
	Choice string
	// Score is the backend's score for a Score question. Levels start at zero,
	// and a score can fall between two levels.
	Score *float64

	// Confidence preserves the backend's statistic when reported. It is
	// neither an action threshold nor necessarily comparable across backends.
	Confidence *float64
	// Probabilities contains the reported probabilities for option IDs. It can be
	// nil. An absent entry is not a reported zero.
	Probabilities map[string]float64
}
