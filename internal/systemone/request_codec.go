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
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

type questionType string

const (
	questionTypeBinary questionType = "noul"
	questionTypeChoice questionType = "choice"
	questionTypeScore  questionType = "score"
)

type questionWire struct {
	Type         questionType    `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

type questionSpec struct {
	questionType questionType
	labels       map[string]struct{}
	levels       int
}

func encodeRequest(req *Request, defaultModel string) ([]byte, map[string]questionSpec, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("request is nil")
	}
	if len(req.Questions) == 0 {
		return nil, nil, fmt.Errorf("at least one question is required")
	}
	state, err := encodeState(req.State)
	if err != nil {
		return nil, nil, fmt.Errorf("state: %w", err)
	}
	wire := struct {
		Model     string                  `json:"model,omitempty"`
		State     json.RawMessage         `json:"state"`
		Questions map[string]questionWire `json:"questions"`
	}{Model: req.Model, State: state, Questions: make(map[string]questionWire, len(req.Questions))}
	if wire.Model == "" {
		wire.Model = defaultModel
	}

	specs := make(map[string]questionSpec, len(req.Questions))
	for id, q := range req.Questions {
		if err := validateRequiredText(id); err != nil {
			return nil, nil, fmt.Errorf("question id %q: %w", id, err)
		}
		encoded, spec, err := encodeQuestion(q)
		if err != nil {
			return nil, nil, fmt.Errorf("question %q: %w", id, err)
		}
		if err := validateRequiredText(encoded.Instructions); err != nil {
			return nil, nil, fmt.Errorf("question %q instructions: %w", id, err)
		}
		wire.Questions[id], specs[id] = encoded, spec
	}
	body, err := json.Marshal(wire)
	return body, specs, err
}

func encodeQuestion(question Question) (questionWire, questionSpec, error) {
	switch q := question.(type) {
	case BinaryQuestion:
		return encodeBinaryQuestion(q)
	case ChoiceQuestion:
		return encodeChoiceQuestion(q)
	case ScoreQuestion:
		return encodeScoreQuestion(q)
	case *BinaryQuestion:
		if q != nil {
			return encodeBinaryQuestion(*q)
		}
	case *ChoiceQuestion:
		if q != nil {
			return encodeChoiceQuestion(*q)
		}
	case *ScoreQuestion:
		if q != nil {
			return encodeScoreQuestion(*q)
		}
	case nil:
	default:
		return questionWire{}, questionSpec{}, fmt.Errorf("unsupported question type %T", question)
	}
	return questionWire{}, questionSpec{}, fmt.Errorf("question is nil")
}

func encodeBinaryQuestion(q BinaryQuestion) (questionWire, questionSpec, error) {
	wire := questionWire{Type: questionTypeBinary, Instructions: q.Instructions}
	spec := questionSpec{questionType: wire.Type}
	if q.Criteria == nil || (q.Criteria.True == "" && q.Criteria.False == "") {
		return wire, spec, nil
	}
	if !utf8.ValidString(q.Criteria.True) || !utf8.ValidString(q.Criteria.False) {
		return wire, spec, fmt.Errorf("binary criteria must be valid UTF-8")
	}
	criteria := struct {
		True  string `json:"true,omitempty"`
		False string `json:"false,omitempty"`
	}{True: q.Criteria.True, False: q.Criteria.False}
	var err error
	wire.Criteria, err = json.Marshal(criteria)
	return wire, spec, err
}

func encodeChoiceQuestion(q ChoiceQuestion) (questionWire, questionSpec, error) {
	wire := questionWire{Type: questionTypeChoice, Instructions: q.Instructions}
	spec := questionSpec{questionType: wire.Type, labels: make(map[string]struct{}, len(q.Options))}
	if len(q.Options) == 0 {
		return wire, spec, fmt.Errorf("choice requires at least one option")
	}
	// A map would sort names during JSON encoding, losing presentation order.
	var criteria bytes.Buffer
	criteria.WriteByte('{')
	for i, option := range q.Options {
		if err := validateRequiredText(option.Name); err != nil {
			return wire, spec, fmt.Errorf("choice name %q: %w", option.Name, err)
		}
		if _, exists := spec.labels[option.Name]; exists {
			return wire, spec, fmt.Errorf("duplicate choice name %q", option.Name)
		}
		if !utf8.ValidString(option.Description) {
			return wire, spec, fmt.Errorf("choice %q description is not valid UTF-8", option.Name)
		}
		spec.labels[option.Name] = struct{}{}
		value := []byte("null")
		if option.Description != "" {
			value, _ = json.Marshal(option.Description)
		}
		if i > 0 {
			criteria.WriteByte(',')
		}
		key, _ := json.Marshal(option.Name)
		criteria.Write(key)
		criteria.WriteByte(':')
		criteria.Write(value)
	}
	criteria.WriteByte('}')
	wire.Criteria = criteria.Bytes()
	return wire, spec, nil
}

func encodeScoreQuestion(q ScoreQuestion) (questionWire, questionSpec, error) {
	wire := questionWire{Type: questionTypeScore, Instructions: q.Instructions}
	spec := questionSpec{questionType: wire.Type, levels: len(q.Criteria)}
	if len(q.Criteria) == 0 {
		return wire, spec, fmt.Errorf("score requires at least one level")
	}
	for i, value := range q.Criteria {
		if err := validateRequiredText(value); err != nil {
			return wire, spec, fmt.Errorf("score level %d: %w", i, err)
		}
	}
	var err error
	wire.Criteria, err = json.Marshal(q.Criteria)
	return wire, spec, err
}

func validateRequiredText(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("text is not valid UTF-8")
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("text must not be blank")
	}
	return nil
}

// Custom MarshalJSON methods can have state, so encode only once and inspect
// the resulting JSON shape rather than the caller's Go type.
func encodeState(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimSpace(data)
	switch data[0] {
	case '"', '{', '[':
		return data, nil
	default:
		return nil, fmt.Errorf("expected a JSON string, object, or array")
	}
}
