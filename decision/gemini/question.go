//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/decision"
)

var questionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

func criterion(o decision.Option) string {
	if o.Description == "" {
		return o.ID
	}
	return o.ID + ": " + o.Description
}

func encodeQuestion(q decision.Question) (question, error) {
	v := question{Type: string(q.Kind), Instructions: q.Instructions}
	if !questionID.MatchString(q.ID) {
		return v, fmt.Errorf("invalid question ID")
	}
	if len(q.Options) > 255 {
		return v, fmt.Errorf("maximum 255 options")
	}
	switch q.Kind {
	case decision.Predicate:
		v.Type = "noul"
	case decision.Choice:
		choices := make(map[string]string, len(q.Options))
		for _, o := range q.Options {
			if !validChoiceID(o.ID) {
				return v, fmt.Errorf("invalid choice ID %q", o.ID)
			}
			choices[o.ID] = o.Description
		}
		v.Criteria = choices
	case decision.Score:
		levels := make([]string, 0, len(q.Options))
		seen := make(map[string]bool, len(q.Options))
		for _, o := range q.Options {
			s := criterion(o)
			if seen[s] {
				return v, fmt.Errorf("duplicate score criterion %q", s)
			}
			seen[s] = true
			levels = append(levels, s)
		}
		v.Criteria = levels
	}
	return v, nil
}

func validChoiceID(id string) bool {
	if len(id) > 512 || utf8.RuneCountInString(id) > 128 || strings.TrimSpace(id) != id {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) || (r != ' ' && unicode.IsSpace(r)) {
			return false
		}
	}
	return true
}
