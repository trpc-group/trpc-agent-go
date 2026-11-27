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
	"math"
	"strconv"
)

// Pointers distinguish missing/null required values from legitimate zeros.
type answerWire struct {
	Type          questionType        `json:"type"`
	Noul          *float64            `json:"noul"`
	Choice        *string             `json:"choice"`
	Score         *float64            `json:"score"`
	Confidence    *float64            `json:"confidence"`
	Legend        map[string]*string  `json:"legend"`
	Probabilities map[string]*float64 `json:"probabilities"`
}

func decodeResponse(body []byte, questions map[string]questionSpec) (*Response, error) {
	var wire struct {
		Model   string                     `json:"model"`
		Answers map[string]json.RawMessage `json:"answers"`
		Usage   Usage                      `json:"usage"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	if wire.Model == "" {
		return nil, fmt.Errorf("missing model")
	}
	if len(wire.Answers) != len(questions) {
		return nil, fmt.Errorf("answer count does not match question count")
	}
	for _, count := range []*int{wire.Usage.InputTokens, wire.Usage.OutputTokens} {
		if count != nil && *count < 0 {
			return nil, fmt.Errorf("negative token count")
		}
	}
	result := &Response{Model: wire.Model, Usage: wire.Usage,
		Answers: make(map[string]Answer, len(questions)), Raw: json.RawMessage(body)}
	for id, spec := range questions {
		raw, ok := wire.Answers[id]
		if !ok {
			return nil, fmt.Errorf("missing answer for question %q", id)
		}
		answer, err := decodeAnswer(raw, spec)
		if err != nil {
			return nil, fmt.Errorf("answer %q: %w", id, err)
		}
		result.Answers[id] = answer
	}
	return result, nil
}

func decodeAnswer(raw json.RawMessage, spec questionSpec) (Answer, error) {
	var wire answerWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	if wire.Type != spec.questionType {
		return nil, fmt.Errorf("type %q does not match %q", wire.Type, spec.questionType)
	}
	if wire.Type == questionTypeBinary {
		if !isProbability(wire.Noul) {
			return nil, fmt.Errorf("missing or invalid noul probability")
		}
		return &BinaryAnswer{Probability: *wire.Noul}, nil
	}
	if !isProbability(wire.Confidence) {
		return nil, fmt.Errorf("missing or invalid confidence")
	}
	if err := validateDistribution(wire.Probabilities); err != nil {
		return nil, err
	}
	if wire.Type == questionTypeChoice {
		return decodeChoice(wire, spec)
	}
	if wire.Type == questionTypeScore {
		return decodeScore(wire, spec)
	}
	return nil, fmt.Errorf("unknown question type %q", wire.Type)
}

func decodeChoice(wire answerWire, spec questionSpec) (*ChoiceAnswer, error) {
	if wire.Choice == nil {
		return nil, fmt.Errorf("missing choice")
	}
	if _, ok := spec.labels[*wire.Choice]; !ok {
		return nil, fmt.Errorf("choice was not requested")
	}
	if len(wire.Probabilities) != len(spec.labels) {
		return nil, fmt.Errorf("choice probabilities do not match requested options")
	}
	probabilities := make(map[string]float64, len(spec.labels))
	for name := range spec.labels {
		value, ok := wire.Probabilities[name]
		if !ok {
			return nil, fmt.Errorf("missing probability for choice %q", name)
		}
		probabilities[name] = *value
	}
	return &ChoiceAnswer{Choice: *wire.Choice, Confidence: *wire.Confidence,
		Probabilities: probabilities}, nil
}

func decodeScore(wire answerWire, spec questionSpec) (*ScoreAnswer, error) {
	if wire.Score == nil || !finite(*wire.Score) || *wire.Score < 0 ||
		*wire.Score > float64(spec.levels-1) {
		return nil, fmt.Errorf("missing or out-of-range score")
	}
	if len(wire.Legend) != spec.levels || len(wire.Probabilities) != spec.levels {
		return nil, fmt.Errorf("score levels do not match requested criteria")
	}
	answer := &ScoreAnswer{Score: *wire.Score, Confidence: *wire.Confidence,
		Levels: make([]ScoreLevel, spec.levels)}
	for i := 0; i < spec.levels; i++ {
		key := strconv.Itoa(i)
		p, ok := wire.Probabilities[key]
		if !ok {
			return nil, fmt.Errorf("missing probability for score level %d", i)
		}
		description := wire.Legend[key]
		if description == nil {
			return nil, fmt.Errorf("missing or null legend for score level %d", i)
		}
		answer.Levels[i] = ScoreLevel{Description: *description, Probability: *p}
	}
	return answer, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func isProbability(value *float64) bool {
	return value != nil && finite(*value) && *value >= 0 && *value <= 1
}

func validateDistribution(probabilities map[string]*float64) error {
	if len(probabilities) == 0 {
		return fmt.Errorf("missing probabilities")
	}
	var sum float64
	for _, value := range probabilities {
		if !isProbability(value) {
			return fmt.Errorf("invalid probability")
		}
		sum += *value
	}
	// Laya rounds each probability to four decimal places independently.
	if math.Abs(sum-1) > 0.0001*float64(len(probabilities))+1e-6 {
		return fmt.Errorf("probabilities do not sum to one")
	}
	return nil
}
