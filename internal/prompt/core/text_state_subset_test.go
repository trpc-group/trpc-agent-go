//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptcore

import (
	"reflect"
	"strings"
	"testing"
)

func TestRender_PreservesSupportedDoubleBraceUnknowns(t *testing.T) {
	rendered, err := Render(
		"{{name}} {{unknown:name}} {{unknown:name?}} {{invalid-name}}",
		SyntaxModeMixedBrace,
		Env{},
		PreserveUnknown,
		WithAcceptName(stateSubsetAcceptName),
	)
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	const want = "{{name}} {{unknown:name}} {{unknown:name?}} {{invalid-name}}"
	if rendered != want {
		t.Fatalf("Render: got %q, want %q", rendered, want)
	}
}

func TestRender_SingleBraceWhitespaceStaysLiteral(t *testing.T) {
	rendered, err := Render(
		"hi { name } and { missing ? }",
		SyntaxModeMixedBrace,
		Env{
			Vars: map[string]string{
				"name": "alice",
			},
		},
		PreserveUnknown,
		WithAcceptName(stateSubsetAcceptName),
	)
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	const want = "hi { name } and { missing ? }"
	if rendered != want {
		t.Fatalf("Render: got %q, want %q", rendered, want)
	}
}

func TestRender_DoubleBraceWhitespaceMatchesStateSubset(t *testing.T) {
	rendered, err := Render(
		"name={{ name }} optional={{ city? }} literal={{ city ? }}",
		SyntaxModeMixedBrace,
		Env{
			Vars: map[string]string{
				"name": "alice",
			},
		},
		PreserveUnknown,
		WithAcceptName(stateSubsetAcceptName),
	)
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	const want = "name=alice optional= literal={{ city ? }}"
	if rendered != want {
		t.Fatalf("Render: got %q, want %q", rendered, want)
	}
}

func TestRender_IncompleteDoubleBraceFallsBackToSingleBrace(t *testing.T) {
	rendered, err := Render(
		"prefix {{name}",
		SyntaxModeMixedBrace,
		Env{
			Vars: map[string]string{
				"name": "alice",
			},
		},
		PreserveUnknown,
		WithAcceptName(stateSubsetAcceptName),
	)
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	const want = "prefix {alice"
	if rendered != want {
		t.Fatalf("Render: got %q, want %q", rendered, want)
	}
}

func TestRender_IncompleteOptionalDoubleBraceCollapsesMissingValue(t *testing.T) {
	rendered, err := Render(
		"prefix {{name?}",
		SyntaxModeMixedBrace,
		Env{},
		PreserveUnknown,
		WithAcceptName(stateSubsetAcceptName),
	)
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	const want = "prefix {"
	if rendered != want {
		t.Fatalf("Render: got %q, want %q", rendered, want)
	}
}

func TestRender_IncompleteOptionalDoubleBraceUsesValueWhenPresent(t *testing.T) {
	rendered, err := Render(
		"prefix {{name?}",
		SyntaxModeMixedBrace,
		Env{
			Vars: map[string]string{
				"name": "alice",
			},
		},
		PreserveUnknown,
		WithAcceptName(stateSubsetAcceptName),
	)
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	const want = "prefix {alice"
	if rendered != want {
		t.Fatalf("Render: got %q, want %q", rendered, want)
	}
}

func TestPlaceholderNames_IgnoresRejectedTokens(t *testing.T) {
	names := PlaceholderNames(
		"{{name}} {{unknown:name}} { title } {artifact.file.txt}",
		SyntaxModeMixedBrace,
		WithAcceptName(stateSubsetAcceptName),
	)

	want := []string{"artifact.file.txt", "name"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("PlaceholderNames: got %v, want %v", names, want)
	}
}

func stateSubsetAcceptName(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "artifact.") {
		return true
	}
	if stateSubsetIdentifier(name) {
		return true
	}

	parts := strings.Split(name, ":")
	if len(parts) != 2 {
		return false
	}
	if !stateSubsetIdentifier(parts[1]) {
		return false
	}

	switch parts[0] {
	case "app", "user", "temp", "invocation":
		return true
	default:
		return false
	}
}

func stateSubsetIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case i == 0 && !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'):
			return false
		case i > 0 && !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'):
			return false
		}
	}
	return true
}
