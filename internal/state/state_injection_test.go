//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package state

import (
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestInjectSessionState(t *testing.T) {
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
			name:        "artifact reference (not implemented)",
			template:    "Content: {artifact.file.txt}",
			state:       map[string]any{},
			expected:    "Content: {artifact.file.txt}", // Should preserve artifact references
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

			result, err := InjectSessionState(tt.template, invocation)

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

func TestInjectSessionStateWithNilInvocation(t *testing.T) {
	template := "Hello {name}!"
	expected := "Hello {name}!" // Should preserve template when no invocation

	result, err := InjectSessionState(template, nil)

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
				t.Errorf("isValidStateName(%q) = %v, expected %v", tt.name, result, tt.expected)
			}
		})
	}
}

func TestNormalizePlaceholders_MustacheToNative(t *testing.T) {
	cases := map[string]string{
		"{{key}}":             "{key}",
		"{{ key }}":           "{key}",
		"{{key?}}":            "{key?}",
		"{{ user:name }}":     "{user:name}",
		"{{app:setting}}":     "{app:setting}",
		"x {{temp:value?}} y": "x {temp:value?} y",
		// invalid names should remain untouched
		"{{invalid-name}}": "{{invalid-name}}",
		"{{ 123abc }}":     "{{ 123abc }}",
	}
	for in, want := range cases {
		got := normalizePlaceholders(in)
		if got != want {
			t.Fatalf("normalizePlaceholders(%q)=%q, want=%q", in, got, want)
		}
	}
}

func TestInjectSessionState_MustachePlaceholders(t *testing.T) {
	// Prepare invocation session state
	sm := make(session.StateMap)
	sm["key"] = []byte(`"v"`)
	sm["user:name"] = []byte(`"alice"`)
	sm["temp:value"] = []byte(`"ctx"`)
	inv := &agent.Invocation{Session: &session.Session{State: sm}}

	// Simple mustache
	s, err := InjectSessionState("hi {{key}}", inv)
	if err != nil || s != "hi v" {
		t.Fatalf("InjectSessionState simple: got %q err=%v", s, err)
	}

	// Namespaced and optional + spaces
	s, err = InjectSessionState("U={{ user:name }}, C={{ temp:value? }}", inv)
	if err != nil || s != "U=alice, C=ctx" {
		t.Fatalf("InjectSessionState ns: got %q err=%v", s, err)
	}

	// Optional missing
	s, err = InjectSessionState("X={{missing?}}.", inv)
	if err != nil || s != "X=." {
		t.Fatalf("InjectSessionState missing optional: got %q err=%v", s, err)
	}

	// Invalid name stays
	s, err = InjectSessionState("bad {{invalid-name}}", inv)
	if err != nil || s != "bad {{invalid-name}}" {
		t.Fatalf("InjectSessionState invalid mustache: got %q err=%v", s, err)
	}
}

func TestInjectSessionState_RawNumericString(t *testing.T) {
	// Prepare invocation session state with a raw numeric-looking string value.
	sm := make(session.StateMap)
	sm["code"] = []byte("123456789012345678901234567890")
	inv := &agent.Invocation{Session: &session.Session{State: sm}}

	s, err := InjectSessionState("Code: {code}", inv)
	if err != nil {
		t.Fatalf("InjectSessionState raw numeric string: unexpected error: %v", err)
	}
	const want = "Code: 123456789012345678901234567890"
	if s != want {
		t.Fatalf("InjectSessionState raw numeric string: got %q, want %q", s, want)
	}
}

func TestInjectSessionState_JSONObjectAndArray(t *testing.T) {
	sm := make(session.StateMap)
	sm["obj"] = []byte(`{"a":1,"b":[2,3]}`)
	sm["arr"] = []byte(`[{"x":1},{"x":2}]`)
	inv := &agent.Invocation{Session: &session.Session{State: sm}}

	got, err := InjectSessionState("O={obj}; A={arr}", inv)
	if err != nil {
		t.Fatalf("InjectSessionState json: unexpected error: %v", err)
	}

	const want = `O={"a":1,"b":[2,3]}; A=[{"x":1},{"x":2}]`
	if got != want {
		t.Fatalf("InjectSessionState json: got %q, want %q", got, want)
	}
}
