//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package decision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Validate checks the common request contract without changing the request.
// A backend may impose additional limits.
func (r Request) Validate() error {
	if err := validateInput(r.Input); err != nil {
		return err
	}
	if len(r.Questions) == 0 {
		return fmt.Errorf("decision: at least one question is required")
	}
	ids := make(map[string]struct{}, len(r.Questions))
	for _, q := range r.Questions {
		if !nonblank(q.ID) || !nonblank(q.Instructions) {
			return fmt.Errorf("decision: question %q requires a nonblank utf-8 id and instructions", q.ID)
		}
		if _, ok := ids[q.ID]; ok {
			return fmt.Errorf("decision: duplicate question id %q", q.ID)
		}
		ids[q.ID] = struct{}{}
		if err := validateOptions(q); err != nil {
			return fmt.Errorf("decision: question %q: %w", q.ID, err)
		}
	}
	return nil
}

func validateInput(input Input) error {
	if input.JSON == nil && nonblank(input.Text) {
		return nil
	}
	if input.Text != "" || input.JSON == nil {
		return fmt.Errorf("decision: provide exactly one nonblank utf-8 text or json input")
	}
	if !utf8.Valid(input.JSON) || !json.Valid(input.JSON) {
		return fmt.Errorf("decision: input is not valid utf-8 json")
	}
	raw := bytes.TrimSpace(input.JSON)
	switch raw[0] {
	case '{':
		return nil
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return fmt.Errorf("decision: decode input array: %w", err)
		}
		for i, item := range items {
			if item[0] != '{' {
				return fmt.Errorf("decision: input array item %d is not an object", i)
			}
		}
		return nil
	default:
		return fmt.Errorf("decision: json input must be an object or array of objects")
	}
}

func validateOptions(q Question) error {
	switch q.Kind {
	case Predicate:
		if len(q.Options) != 0 {
			return fmt.Errorf("predicate does not take options")
		}
		return nil
	case Choice, Score:
		if len(q.Options) < 2 {
			return fmt.Errorf("%s requires at least two options", q.Kind)
		}
	default:
		return fmt.Errorf("unsupported kind %q", q.Kind)
	}
	ids := make(map[string]struct{}, len(q.Options))
	for _, opt := range q.Options {
		if !nonblank(opt.ID) || !utf8.ValidString(opt.Description) {
			return fmt.Errorf("invalid option %q", opt.ID)
		}
		if _, ok := ids[opt.ID]; ok {
			return fmt.Errorf("duplicate option id %q", opt.ID)
		}
		ids[opt.ID] = struct{}{}
	}
	return nil
}

func nonblank(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != ""
}
