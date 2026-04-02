//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package state provides state-backed prompt rendering adapters.
package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	promptcore "trpc.group/trpc-go/trpc-agent-go/internal/prompt/core"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// Prefix for current invocation-scoped state variables from invocation.state
	stateInvocationKey = "invocation:"
)

// Option configures [Render].
type Option func(*renderConfig)

type renderConfig struct {
	session *session.Session
}

// WithSession overrides the session used for non-invocation placeholders.
// {invocation:*} placeholders continue to read from invocation state.
func WithSession(sess *session.Session) Option {
	return func(cfg *renderConfig) {
		cfg.session = sess
	}
}

// Render replaces supported placeholders in template with values from
// invocation state and session state.
//
// This adapter accepts the legacy state-injection subset of mixed-brace
// placeholders:
//   - {name} or {{name}} for bare identifiers
//   - {name?} or {{name?}} for optional identifiers
//   - {app:key}, {user:key}, or {temp:key} for namespaced session state
//   - {invocation:key} for invocation-scoped state
//   - {artifact.filename} or {artifact.filename?} for artifact references
//
// {invocation:*} placeholders are resolved only from invocation state. Other
// supported placeholders are resolved from invocation.Session. Supported
// optional placeholders collapse to an empty string when unresolved, while
// unresolved non-optional placeholders remain literal. Placeholders outside
// this subset remain literal.
//
// Example:
//
//	template: "Tell me about the city stored in {capital_city}."
//	state: {"capital_city": "Paris"}
//	result: "Tell me about the city stored in Paris."
func Render(
	template string,
	invocation *agent.Invocation,
	opts ...Option,
) (string, error) {
	cfg := renderConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return render(template, invocation, cfg.session)
}

func render(
	template string,
	invocation *agent.Invocation,
	sess *session.Session,
) (string, error) {
	if template == "" {
		return template, nil
	}

	resolver := stateResolver{
		invocation: invocation,
		session:    sess,
	}
	rendered, err := promptcore.Render(
		template,
		promptcore.SyntaxModeMixedBrace,
		promptcore.Env{
			Resolve: func(name string) (string, bool, error) {
				return resolver.Resolve(name)
			},
		},
		promptcore.PreserveUnknown,
		promptcore.WithAcceptName(isValidStateName),
	)
	if err != nil {
		return template, err
	}
	return rendered, nil
}

type stateResolver struct {
	invocation *agent.Invocation
	session    *session.Session
}

func (r stateResolver) Resolve(name string) (string, bool, error) {
	if stateKey, ok := strings.CutPrefix(name, stateInvocationKey); ok {
		if r.invocation != nil {
			if val, exists := r.invocation.GetState(stateKey); exists && val != nil {
				return fmt.Sprintf("%+v", val), true, nil
			}
		}
		return "", false, nil
	}

	// Get the value from session state.
	sessionToUse := r.session
	if sessionToUse == nil && r.invocation != nil {
		sessionToUse = r.invocation.Session
	}
	if sessionToUse != nil {
		if jsonBytes, exists := sessionToUse.GetState(name); exists {
			return renderStateValue(jsonBytes), true, nil
		}
	}

	return "", false, nil
}

// renderStateValue converts a raw state value to its string representation.
// It preserves JSON semantics while avoiding scientific notation and precision
// issues for numeric literals by decoding them into json.Number.
func renderStateValue(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if !json.Valid(raw) {
		return string(raw)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var jsonValue any
	if err := dec.Decode(&jsonValue); err != nil {
		return string(raw)
	}
	switch v := jsonValue.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		// Preserve JSON objects/arrays as JSON text so injection does not
		// degrade them into Go's fmt representation (e.g. map[k:v]).
		return string(raw)
	}
}

// isValidStateName checks whether a placeholder name belongs to the legacy
// state-injection subset.
//
// Keeping this narrower than the public prompt grammar preserves historical
// behavior: placeholders outside the old subset remain literal in the state
// adapter, including optional placeholders.
func isValidStateName(varName string) bool {
	if varName == "" {
		return false
	}

	if strings.HasPrefix(varName, "artifact.") {
		return true
	}

	if isIdentifier(varName) {
		return true
	}

	// Check if it has a prefix.
	parts := strings.Split(varName, ":")
	if len(parts) == 2 {
		prefix := parts[0] + ":"
		validPrefixes := []string{
			session.StateAppPrefix,
			session.StateUserPrefix,
			session.StateTempPrefix,
			stateInvocationKey,
		}
		for _, validPrefix := range validPrefixes {
			if prefix == validPrefix {
				return isIdentifier(parts[1])
			}
		}
	}

	return false
}

// isIdentifier checks if the string is a valid Go identifier.
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	// First character must be a letter or underscore.
	if !isLetterOrUnderscore(rune(s[0])) {
		return false
	}
	// All other characters must be letters, digits, or underscores.
	for _, r := range s[1:] {
		if !isLetterOrDigitOrUnderscore(r) {
			return false
		}
	}
	return true
}

// isLetterOrUnderscore checks if the rune is a letter or underscore.
func isLetterOrUnderscore(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

// isLetterOrDigitOrUnderscore checks if the rune is a letter, digit, or underscore.
func isLetterOrDigitOrUnderscore(r rune) bool {
	return isLetterOrUnderscore(r) || (r >= '0' && r <= '9')
}
