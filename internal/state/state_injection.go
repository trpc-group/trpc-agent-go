//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package state provides state injection functionality.
package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/prompt"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// Prefix for current invocation-scoped state variables from invocation.state
	stateInvocationKey = "invocation:"
)

// mustachePlaceholderRE matches Mustache-style placeholders like {{key}},
// optionally allowing namespaces (user:, app:, temp:) and the optional
// suffix '?' (e.g., {{key?}}, {{temp:value}}). It purposely restricts the
// allowed characters to avoid over-replacing in free text.
var mustachePlaceholderRE = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*:(?:[A-Za-z_][A-Za-z0-9_]*)|[A-Za-z_][A-Za-z0-9_]*)(\?)?\s*\}\}`)

// normalizePlaceholders converts supported Mustache-style placeholders
// to the framework's native single-brace form before injection.
// Examples:
//
//	{{key}}          -> {key}
//	{{key?}}         -> {key?}
//	{{user:name}}    -> {user:name}
//	{{temp:value?}}  -> {temp:value?}
func normalizePlaceholders(s string) string {
	if s == "" {
		return s
	}
	return mustachePlaceholderRE.ReplaceAllString(s, `{$1$2}`)
}

// InjectSessionState replaces state variables in the instruction template with their corresponding values from session state.
// This function supports the following patterns:
// - {variable_name}: Replaces with the value of the variable from session state.
// - {variable_name?}: Optional variable, replaces with empty string if not found.
// - {artifact.filename}: Replaces with artifact content (not implemented yet).
//
// Example:
//
//	template: "Tell me about the city stored in {capital_city}."
//	state: {"capital_city": "Paris"}
//	result: "Tell me about the city stored in Paris."
func InjectSessionState(template string, invocation *agent.Invocation) (string, error) {
	return injectSessionState(template, invocation, nil)
}

// InjectSessionStateWithSession injects state into template using invocation
// state and an explicit session override.
//
// This is useful when the caller wants to read placeholders from a session
// object that is not attached to the invocation, while still supporting
// {invocation:*} placeholders.
//
// Precedence:
//   - {invocation:*} reads from invocation state (invocation.GetState)
//   - other placeholders read from the provided session when non-nil;
//     otherwise from invocation.Session
func InjectSessionStateWithSession(
	template string,
	invocation *agent.Invocation,
	sess *session.Session,
) (string, error) {
	return injectSessionState(template, invocation, sess)
}

func injectSessionState(
	template string,
	invocation *agent.Invocation,
	sess *session.Session,
) (string, error) {
	rendered, err := prompt.Text{Template: template}.Render(prompt.RenderEnv{
		Resolver: stateResolver{
			invocation: invocation,
			session:    sess,
		},
	})
	if err != nil {
		return template, err
	}
	return rendered, nil
}

type stateResolver struct {
	invocation *agent.Invocation
	session    *session.Session
}

func (r stateResolver) Resolve(ref prompt.Ref) (string, bool, error) {
	switch ref.Namespace {
	case stateInvocationKey[:len(stateInvocationKey)-1]:
		if r.invocation == nil {
			return "", false, nil
		}
		if val, exists := r.invocation.GetState(ref.Name); exists && val != nil {
			return fmt.Sprintf("%+v", val), true, nil
		}
		return "", false, nil
	case "", session.StateAppPrefix[:len(session.StateAppPrefix)-1],
		session.StateUserPrefix[:len(session.StateUserPrefix)-1],
		session.StateTempPrefix[:len(session.StateTempPrefix)-1]:
		sessionToUse := r.session
		if sessionToUse == nil && r.invocation != nil {
			sessionToUse = r.invocation.Session
		}
		if sessionToUse == nil {
			return "", false, nil
		}
		key := ref.Name
		if ref.Namespace != "" {
			key = ref.Namespace + ":" + ref.Name
		}
		jsonBytes, exists := sessionToUse.GetState(key)
		if !exists {
			return "", false, nil
		}
		return renderStateValue(jsonBytes), true, nil
	default:
		return "", false, nil
	}
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

// isValidStateName checks if the variable name is a valid state name.
// Valid state names are either:
// - Valid identifiers (alphanumeric + underscore, starting with letter or underscore)
// - Names with prefixes like "app:", "user:", "temp:" followed by valid identifiers
func isValidStateName(varName string) bool {
	if varName == "" {
		return false
	}

	// Check if it's a simple identifier.
	if isIdentifier(varName) {
		return true
	}

	// Check if it has a prefix.
	parts := strings.Split(varName, ":")
	if len(parts) == 2 {
		prefix := parts[0] + ":"
		validPrefixes := []string{session.StateAppPrefix, session.StateUserPrefix, session.StateTempPrefix, stateInvocationKey}
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
