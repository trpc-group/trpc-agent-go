//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package systemone

import (
	"encoding/json"
	"fmt"
)

// Response contains all answers to a successful request. The caller owns its
// maps, slices, raw bytes, and answer objects. Concurrent reads are safe when
// no caller modifies this data. Changes to typed fields and Raw are not synced.
// A Response is decoded by Client; marshaling it does not produce wire JSON.
type Response struct {
	Model     string
	Answers   map[string]Answer
	Usage     Usage
	RequestID string
	// Raw retains the complete JSON response, including provider extensions
	// such as routing, abstention, action scores, and truncation evidence.
	Raw json.RawMessage
}

// Usage contains reported token counts and optional input truncation evidence.
// Nil fields mean unknown, not zero or false. Token accounting differs by server.
type Usage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
	// Truncated is absent from Jev and strict Laya responses.
	Truncated *bool `json:"truncated"`
}

// Answer is a decoded *BinaryAnswer, *ChoiceAnswer, or *ScoreAnswer. Its concrete
// type matches the request question. The unexported method marks answer types;
// use Response.Binary, Response.Choice, or Response.Score for checked lookup.
type Answer interface {
	isAnswer()
}

// BinaryAnswer reports P(true), without applying a decision or approval threshold.
type BinaryAnswer struct {
	Probability float64
}

func (*BinaryAnswer) isAnswer() {}

// ChoiceAnswer reports the selected label and its distribution. Confidence is
// the provider's original statistic: Jev and Laya compute it differently, so
// thresholds cannot be transferred between providers without validation.
type ChoiceAnswer struct {
	Choice        string
	Confidence    float64
	Probabilities map[string]float64
}

func (*ChoiceAnswer) isAnswer() {}

// ScoreAnswer reports the expected ordinal level, which can be fractional.
// Levels are ordered by score starting at zero. Confidence retains provider
// semantics as in ChoiceAnswer. Level descriptions must be JSON strings.
type ScoreAnswer struct {
	Score      float64
	Confidence float64
	Levels     []ScoreLevel
}

func (*ScoreAnswer) isAnswer() {}

// ScoreLevel pairs a returned level description with its probability. Its
// position in ScoreAnswer.Levels is its ordinal score. Descriptions are returned
// unchanged and need not be byte-identical to the request's criteria.
type ScoreLevel struct {
	Description string
	Probability float64
}

// Binary returns the binary answer for id, borrowing the object in Answers.
// It returns an error for a nil response, absent ID, nil answer, or wrong type.
// It does not decode Raw or perform a network call.
func (r *Response) Binary(id string) (*BinaryAnswer, error) {
	return answerAs[*BinaryAnswer](r, id)
}

// Choice returns the choice answer for id, borrowing the object in Answers,
// including its probability map. It returns an error for a nil response,
// absent ID, nil answer, or wrong type. It does not decode Raw or call a server.
func (r *Response) Choice(id string) (*ChoiceAnswer, error) {
	return answerAs[*ChoiceAnswer](r, id)
}

// Score returns the score answer for id, borrowing the object in Answers,
// including its Levels slice. It returns an error for a nil response, absent ID,
// nil answer, or wrong type. It does not decode Raw or call a server.
func (r *Response) Score(id string) (*ScoreAnswer, error) {
	return answerAs[*ScoreAnswer](r, id)
}

func answerAs[T interface {
	*BinaryAnswer | *ChoiceAnswer | *ScoreAnswer
}](r *Response, id string) (T, error) {
	if r == nil {
		return nil, fmt.Errorf("systemone: answer %q: response is nil", id)
	}
	value, ok := r.Answers[id]
	if !ok {
		return nil, fmt.Errorf("systemone: answer %q not found", id)
	}
	var expected T
	answer, ok := value.(T)
	if !ok {
		return nil, fmt.Errorf("systemone: answer %q: expected %T, got %T", id, expected, value)
	}
	if answer == nil {
		return nil, fmt.Errorf("systemone: answer %q is nil", id)
	}
	return answer, nil
}
