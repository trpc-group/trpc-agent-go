//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package state

import (
	"encoding/json"
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name        string
		template    string
		state       map[string]any
		expected    string
		expectError bool
		invState    map[string]any
	}{
		{
			name:        "empty template",
			template:    "",
			state:       map[string]any{},
			expected:    "",
			expectError: false,
		},
		{
			name:        "no state variables",
			template:    "Hello, world!",
			state:       map[string]any{},
			expected:    "Hello, world!",
			expectError: false,
		},
		{
			name:        "simple state variable",
			template:    "Tell me about {capital_city}.",
			state:       map[string]any{"capital_city": "Paris"},
			expected:    "Tell me about Paris.",
			expectError: false,
		},
		{
			name:        "multiple state variables",
			template:    "The capital of {country} is {capital_city}.",
			state:       map[string]any{"country": "France", "capital_city": "Paris"},
			expected:    "The capital of France is Paris.",
			expectError: false,
		},
		{
			name:        "optional variable present",
			template:    "Hello {name?}!",
			state:       map[string]any{"name": "Alice"},
			expected:    "Hello Alice!",
			expectError: false,
		},
		{
			name:        "optional variable missing",
			template:    "Hello {name?}!",
			state:       map[string]any{},
			expected:    "Hello !",
			expectError: false,
		},
		{
			name:        "non-optional variable missing",
			template:    "Hello {name}!",
			state:       map[string]any{},
			expected:    "Hello {name}!", // Should preserve the template
			expectError: false,
		},
		{
			name:        "mixed optional and non-optional",
			template:    "Hello {name?}, your age is {age}.",
			state:       map[string]any{"age": 25},
			expected:    "Hello , your age is 25.",
			expectError: false,
		},
		{
			name:        "invalid variable name",
			template:    "Hello {invalid-name}!",
			state:       map[string]any{},
			expected:    "Hello {invalid-name}!", // Should preserve invalid names
			expectError: false,
		},
		{
			name:        "invalid optional variable name",
			template:    "Hello {invalid-name?}!",
			state:       map[string]any{},
			expected:    "Hello {invalid-name?}!", // Invalid names stay literal even when optional.
			expectError: false,
		},
		{
			name:        "numeric leading variable name remains literal",
			template:    "Hello {123invalid}!",
			state:       map[string]any{"123invalid": "value"},
			expected:    "Hello {123invalid}!",
			expectError: false,
		},
		{
			name:        "artifact reference (not implemented)",
			template:    "Content: {artifact.file.txt}",
			state:       map[string]any{},
			expected:    "Content: {artifact.file.txt}", // Should preserve artifact references
			expectError: false,
		},
		{
			name:        "optional artifact reference (not implemented)",
			template:    "Content: {artifact.file.txt?}",
			state:       map[string]any{},
			expected:    "Content: ", // Optional artifact references collapse to empty.
			expectError: false,
		},
		{
			name:        "prefixed variable names",
			template:    "User: {user:preference}, App: {app:setting}",
			state:       map[string]any{"user:preference": "dark", "app:setting": "enabled"},
			expected:    "User: dark, App: enabled",
			expectError: false,
		},
		{
			name:        "numeric values",
			template:    "Count: {count}, Price: {price}",
			state:       map[string]any{"count": 42, "price": 19.99},
			expected:    "Count: 42, Price: 19.99",
			expectError: false,
		},
		{
			name:        "boolean values",
			template:    "Enabled: {enabled}, Active: {active}",
			state:       map[string]any{"enabled": true, "active": false},
			expected:    "Enabled: true, Active: false",
			expectError: false,
		},
		{
			name:        "invocation values",
			template:    "Enabled: {invocation:enabled}, name: {invocation:name}",
			invState:    map[string]any{"enabled": true, "name": "name-123"},
			expected:    "Enabled: true, name: name-123",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert interface{} map to StateMap (map[string][]byte).
			stateMap := make(session.StateMap)
			for k, v := range tt.state {
				if jsonBytes, err := json.Marshal(v); err == nil {
					stateMap[k] = jsonBytes
				}
			}

			// Create a mock invocation with the test state.
			invocation := &agent.Invocation{
				Session: &session.Session{
					State: stateMap,
				},
			}
			if tt.invState != nil {
				for k, v := range tt.invState {
					invocation.SetState(k, v)
				}
			}

			result, err := Render(tt.template, invocation)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestRenderWithNilInvocation(t *testing.T) {
	template := "Hello {name}!"
	expected := "Hello {name}!" // Should preserve template when no invocation

	result, err := Render(template, nil)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestIsValidStateName(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"valid_name", true},
		{"validName", true},
		{"valid123", true},
		{"_valid", true},
		{"user:preference", true},
		{"app:setting", true},
		{"temp:value", true},
		{"invocation:key", true},
		{"invalid-name", false},
		{"invalid name", false},
		{"123invalid", false},
		{"", false},
		{"user:", false},
		{":value", false},
		{"user:invalid-name", false},
		{"unknown:prefix", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidStateName(tt.name)
			if result != tt.expected {
				t.Errorf(
					"isValidStateName(%q) = %v, expected %v",
					tt.name,
					result,
					tt.expected,
				)
			}
		})
	}
}

func TestRender_MustachePlaceholders(t *testing.T) {
	// Prepare invocation session state
	sm := make(session.StateMap)
	sm["key"] = []byte(`"v"`)
	sm["user:name"] = []byte(`"alice"`)
	sm["temp:value"] = []byte(`"ctx"`)
	inv := &agent.Invocation{Session: &session.Session{State: sm}}

	// Simple mustache
	s, err := Render("hi {{key}}", inv)
	if err != nil || s != "hi v" {
		t.Fatalf("Render simple: got %q err=%v", s, err)
	}

	// Namespaced and optional + spaces
	s, err = Render("U={{ user:name }}, C={{ temp:value? }}", inv)
	if err != nil || s != "U=alice, C=ctx" {
		t.Fatalf("Render ns: got %q err=%v", s, err)
	}

	// Optional missing
	s, err = Render("X={{missing?}}.", inv)
	if err != nil || s != "X=." {
		t.Fatalf("Render missing optional: got %q err=%v", s, err)
	}

	// Invalid name stays
	s, err = Render("bad {{invalid-name}}", inv)
	if err != nil || s != "bad {{invalid-name}}" {
		t.Fatalf("Render invalid mustache: got %q err=%v", s, err)
	}

	// Invalid optional name stays too.
	s, err = Render("bad {{invalid-name?}}", inv)
	if err != nil || s != "bad {{invalid-name?}}" {
		t.Fatalf("Render invalid optional mustache: got %q err=%v", s, err)
	}
}

func TestRender_SingleBracePlaceholdersWithWhitespaceStayLiteral(t *testing.T) {
	sm := make(session.StateMap)
	sm["name"] = []byte(`"alice"`)
	inv := &agent.Invocation{Session: &session.Session{State: sm}}

	s, err := Render("hi { name } and { missing ? }", inv)
	if err != nil {
		t.Fatalf("Render whitespace: got err=%v", err)
	}
	if s != "hi { name } and { missing ? }" {
		t.Fatalf("Render whitespace: got %q", s)
	}
}

func TestRender_StateInjectionScenarios(t *testing.T) {
	tests := []struct {
		name            string
		template        string
		sessionState    map[string]any
		invocationState map[string]any
		overrideState   map[string]any
		want            string
	}{
		{
			name:         "supported double brace resolves",
			template:     "Hello {{name}} from {{user:city}}",
			sessionState: map[string]any{"name": "alice", "user:city": "paris"},
			want:         "Hello alice from paris",
		},
		{
			name:         "supported double brace unknown stays double brace",
			template:     "Hello {{name}} from {{user:city}}",
			sessionState: map[string]any{"name": "alice"},
			want:         "Hello alice from {{user:city}}",
		},
		{
			name:     "unknown prefix double brace stays literal",
			template: "Hello {{unknown:name}} and {{unknown:name?}}",
			want:     "Hello {{unknown:name}} and {{unknown:name?}}",
		},
		{
			name:         "single brace whitespace stays literal",
			template:     "Hello { name } and { missing ? }",
			sessionState: map[string]any{"name": "alice"},
			want:         "Hello { name } and { missing ? }",
		},
		{
			name:         "double brace whitespace still resolves",
			template:     "Hello {{ name }} and {{ user:city? }}",
			sessionState: map[string]any{"name": "alice"},
			want:         "Hello alice and ",
		},
		{
			name:         "incomplete double brace falls back to single brace",
			template:     "Hello {{name}",
			sessionState: map[string]any{"name": "alice"},
			want:         "Hello {alice",
		},
		{
			name:     "incomplete optional double brace collapses missing value",
			template: "Hello {{name?}",
			want:     "Hello {",
		},
		{
			name:         "incomplete optional double brace uses value when present",
			template:     "Hello {{name?}",
			sessionState: map[string]any{"name": "alice"},
			want:         "Hello {alice",
		},
		{
			name:     "double brace space before optional stays literal",
			template: "Hello {{ name ? }}",
			want:     "Hello {{ name ? }}",
		},
		{
			name:            "invocation placeholder does not fallback to session",
			template:        "Hello {{name}}, Case={{invocation:case}}",
			sessionState:    map[string]any{"name": "bob", "invocation:case": "from-session"},
			invocationState: map[string]any{"case": "case-1"},
			want:            "Hello bob, Case=case-1",
		},
		{
			name:            "session override respects precedence",
			template:        "Hello {{name}}, Case={{invocation:case}}",
			sessionState:    map[string]any{"name": "bob"},
			overrideState:   map[string]any{"name": "alice"},
			invocationState: map[string]any{"case": "case-1"},
			want:            "Hello alice, Case=case-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := newTestInvocation(tt.sessionState, tt.invocationState)
			override := newTestSession(tt.overrideState)

			var opts []Option
			if override != nil {
				opts = append(opts, WithSession(override))
			}

			got, err := Render(tt.template, inv, opts...)
			if err != nil {
				t.Fatalf("Render: unexpected error: %v", err)
			}

			if got != tt.want {
				t.Fatalf("Render mismatch: got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRender_RawNumericString(t *testing.T) {
	// Prepare invocation session state with a raw numeric-looking string value.
	sm := make(session.StateMap)
	sm["code"] = []byte("123456789012345678901234567890")
	inv := &agent.Invocation{Session: &session.Session{State: sm}}

	s, err := Render("Code: {code}", inv)
	if err != nil {
		t.Fatalf("Render raw numeric string: unexpected error: %v", err)
	}
	const want = "Code: 123456789012345678901234567890"
	if s != want {
		t.Fatalf("Render raw numeric string: got %q, want %q", s, want)
	}
}

func TestRender_RawNumericPrefixText(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "rfc822_date",
			raw:  "23 Dec 25 18:31 CST",
		},
		{
			name: "iso_date",
			raw:  "2025-12-23",
		},
		{
			name: "number_and_chinese",
			raw:  "23中文",
		},
	}

	const (
		stateKey  = "value"
		template  = "V={value}"
		wantFmt   = "V=%s"
		errPrefix = "Render raw numeric prefix text"
	)

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			sm := make(session.StateMap)
			sm[stateKey] = []byte(tt.raw)
			inv := &agent.Invocation{
				Session: &session.Session{State: sm},
			}

			got, err := Render(template, inv)
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", errPrefix, err)
			}
			want := fmt.Sprintf(wantFmt, tt.raw)
			if got != want {
				t.Fatalf("%s: got %q, want %q", errPrefix, got,
					want)
			}
		})
	}
}

func TestRender_EmptyRawValue(t *testing.T) {
	const (
		stateKey  = "empty"
		template  = "E={empty}"
		want      = "E="
		errPrefix = "Render empty raw value"
	)

	sm := make(session.StateMap)
	sm[stateKey] = nil
	inv := &agent.Invocation{
		Session: &session.Session{State: sm},
	}

	got, err := Render(template, inv)
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", errPrefix, err)
	}
	if got != want {
		t.Fatalf("%s: got %q, want %q", errPrefix, got, want)
	}
}

func TestRender_JSONObjectAndArray(t *testing.T) {
	sm := make(session.StateMap)
	sm["obj"] = []byte(`{"a":1,"b":[2,3]}`)
	sm["arr"] = []byte(`[{"x":1},{"x":2}]`)
	inv := &agent.Invocation{Session: &session.Session{State: sm}}

	got, err := Render("O={obj}; A={arr}", inv)
	if err != nil {
		t.Fatalf("Render json: unexpected error: %v", err)
	}

	const want = `O={"a":1,"b":[2,3]}; A=[{"x":1},{"x":2}]`
	if got != want {
		t.Fatalf("Render json: got %q, want %q", got, want)
	}
}

func TestRender_WithSessionOverride(t *testing.T) {
	const (
		template = "Hello {name}, Case={invocation:case}"
		want     = "Hello Alice, Case=case-1"
	)

	sessState := make(session.StateMap)
	sessState["name"] = []byte(`"Alice"`)
	sess := &session.Session{State: sessState}

	invSessState := make(session.StateMap)
	invSessState["name"] = []byte(`"Bob"`)
	inv := &agent.Invocation{Session: &session.Session{State: invSessState}}
	inv.SetState("case", "case-1")

	got, err := Render(template, inv, WithSession(sess))
	if err != nil {
		t.Fatalf(
			"Render with session override: unexpected error: %v",
			err,
		)
	}
	if got != want {
		t.Fatalf(
			"Render with session override: got %q, want %q",
			got,
			want,
		)
	}
}

func TestRender_InvocationPlaceholderDoesNotFallbackToSession(t *testing.T) {
	const (
		template = "Hello {name}, Case={invocation:case}"
		want     = "Hello Bob, Case={invocation:case}"
	)

	invSessState := make(session.StateMap)
	invSessState["name"] = []byte(`"Bob"`)
	invSessState["invocation:case"] = []byte(`"from-session"`)
	inv := &agent.Invocation{Session: &session.Session{State: invSessState}}

	got, err := Render(template, inv)
	if err != nil {
		t.Fatalf(
			"Render invocation fallback: unexpected error: %v",
			err,
		)
	}
	if got != want {
		t.Fatalf(
			"Render invocation fallback: got %q, want %q",
			got,
			want,
		)
	}
}

func TestRender_WithSessionInvocationPlaceholderDoesNotFallbackToOverride(t *testing.T) {
	const (
		template = "Hello {name}, Case={invocation:case}"
		want     = "Hello Alice, Case={invocation:case}"
	)

	sessState := make(session.StateMap)
	sessState["name"] = []byte(`"Alice"`)
	sessState["invocation:case"] = []byte(`"from-override-session"`)
	sess := &session.Session{State: sessState}

	invSessState := make(session.StateMap)
	invSessState["name"] = []byte(`"Bob"`)
	inv := &agent.Invocation{Session: &session.Session{State: invSessState}}

	got, err := Render(template, inv, WithSession(sess))
	if err != nil {
		t.Fatalf(
			"Render with session invocation fallback: unexpected error: %v",
			err,
		)
	}
	if got != want {
		t.Fatalf(
			"Render with session invocation fallback: got %q, want %q",
			got,
			want,
		)
	}
}

func TestRender_WithSessionNoRecursiveExpansion(t *testing.T) {
	const (
		template = "X={invocation:x}"
		want     = "X={user:name}"
	)

	sessState := make(session.StateMap)
	sessState["user:name"] = []byte(`"Alice"`)
	sess := &session.Session{State: sessState}

	inv := &agent.Invocation{}
	inv.SetState("x", "{user:name}")

	got, err := Render(template, inv, WithSession(sess))
	if err != nil {
		t.Fatalf(
			"Render with session recursion: unexpected error: %v",
			err,
		)
	}
	if got != want {
		t.Fatalf(
			"Render with session recursion: got %q, want %q",
			got,
			want,
		)
	}
}

func newTestInvocation(
	sessionState map[string]any,
	invocationState map[string]any,
) *agent.Invocation {
	inv := &agent.Invocation{
		Session: newTestSession(sessionState),
	}
	for key, value := range invocationState {
		inv.SetState(key, value)
	}
	return inv
}

func newTestSession(values map[string]any) *session.Session {
	if values == nil {
		return nil
	}

	stateMap := make(session.StateMap, len(values))
	for key, value := range values {
		jsonBytes, err := json.Marshal(value)
		if err != nil {
			panic(fmt.Sprintf("marshal test state %q: %v", key, err))
		}
		stateMap[key] = jsonBytes
	}
	return &session.Session{State: stateMap}
}
