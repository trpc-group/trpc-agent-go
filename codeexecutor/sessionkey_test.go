//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

import (
	"regexp"
	"strings"
	"testing"
)

var safeKeyPattern = regexp.MustCompile(`^sess-[0-9a-f]{32}$`)

func TestSessionWorkspaceKeyFilesystemSafe(t *testing.T) {
	longField := strings.Repeat("x", 1000)
	nastyInputs := [][3]string{
		{"app/name", "user:id", "ses/sion"},
		{`C:\path\to\app`, `\\server\share`, `con`},
		{"a|b|c", "u|v", "i|j"},
		{" app ", " user ", " sid "},
		{"appl\x00ication", "us\ter", "ses\nsion"},
		{"应用/名前", "ユーザー:ID", "会话ID"},
		{"app\twith\ttabs", "user\nwith\nnewlines", "id\r\nwith\rcrlf"},
		{longField, longField, longField},
		{"", "", "plain-session-id"},
		{".", "..", "..."},
		{"nul", "aux", "prn"},
	}
	for _, triple := range nastyInputs {
		app, user, id := triple[0], triple[1], triple[2]
		key := SessionWorkspaceKey(app, user, id)
		if !safeKeyPattern.MatchString(key) {
			t.Errorf("SessionWorkspaceKey(%q, %q, %q) = %q, want match %v",
				app, user, id, key, safeKeyPattern.String())
		}
		if strings.ContainsAny(key, `/\:|*?"<> `) {
			t.Errorf("SessionWorkspaceKey(%q, %q, %q) = %q contains unsafe characters",
				app, user, id, key)
		}
	}
}

func TestSessionWorkspaceKeyDeterministic(t *testing.T) {
	app, user, id := "myapp", "myuser", "session-123"
	first := SessionWorkspaceKey(app, user, id)
	second := SessionWorkspaceKey(app, user, id)
	if first == "" {
		t.Fatalf("SessionWorkspaceKey(%q, %q, %q) returned empty", app, user, id)
	}
	if first != second {
		t.Errorf("SessionWorkspaceKey not deterministic: %q vs %q", first, second)
	}
}

func TestSessionWorkspaceKeyInjective(t *testing.T) {
	cases := []struct {
		x, y [3]string
	}{
		{[3]string{"a", "", "b"}, [3]string{"", "a", "b"}},
		{[3]string{"a/b", "c", "d"}, [3]string{"a", "b/c", "d"}},
		{[3]string{"ab", "c", "d"}, [3]string{"a", "bc", "d"}},
	}
	for _, tc := range cases {
		k1 := SessionWorkspaceKey(tc.x[0], tc.x[1], tc.x[2])
		k2 := SessionWorkspaceKey(tc.y[0], tc.y[1], tc.y[2])
		if k1 == k2 {
			t.Errorf("SessionWorkspaceKey collision: (%q,%q,%q) and (%q,%q,%q) both = %q",
				tc.x[0], tc.x[1], tc.x[2], tc.y[0], tc.y[1], tc.y[2], k1)
		}
	}
}

func TestSessionWorkspaceKeyEmptyID(t *testing.T) {
	if got := SessionWorkspaceKey("app", "user", ""); got != "" {
		t.Errorf(`SessionWorkspaceKey("app","user","") = %q, want ""`, got)
	}
	if got := SessionWorkspaceKey("app", "user", "   "); got != "" {
		t.Errorf(`SessionWorkspaceKey("app","user","   ") = %q, want ""`, got)
	}
	if got := SessionWorkspaceKey("", "", "\t\n "); got != "" {
		t.Errorf(`SessionWorkspaceKey("","","\t\n ") = %q, want ""`, got)
	}
}

func TestSessionWorkspaceKeyFixedLength(t *testing.T) {
	longField := strings.Repeat("z", 1000)
	inputs := [][3]string{
		{"a", "b", "c"},
		{longField, longField, longField},
		{strings.Repeat("会话", 2000), strings.Repeat("用户", 2000), strings.Repeat("标识", 2000)},
	}
	for _, triple := range inputs {
		key := SessionWorkspaceKey(triple[0], triple[1], triple[2])
		if len(key) != 37 {
			t.Errorf("SessionWorkspaceKey with app/user/id lengths %d/%d/%d gave %d chars (%q), want 37",
				len(triple[0]), len(triple[1]), len(triple[2]), len(key), key)
		}
	}
}

func TestLegacySessionWorkspaceKey(t *testing.T) {
	cases := []struct {
		app, user, id, want string
	}{
		{"app", "user", "sid", "app/user/sid"},
		{"app", "", "sid", "sid"},
		{"", "user", "sid", "sid"},
		{"", "", "sid", "sid"},
		{"app", "user", "", ""},
		{"app", "user", "   ", ""},
		// Mirror resolver.go LegacyKeyFromInvocation semantics:
		// legacy format is used verbatim (no trimming of a non-empty id).
		{"app", "user", " sid ", "app/user/ sid "},
		{" app ", " user ", "sid", " app / user /sid"},
	}
	for _, tc := range cases {
		got := LegacySessionWorkspaceKey(tc.app, tc.user, tc.id)
		if got != tc.want {
			t.Errorf("LegacySessionWorkspaceKey(%q, %q, %q) = %q, want %q",
				tc.app, tc.user, tc.id, got, tc.want)
		}
	}
}

func TestSessionWorkspaceKeyKnownVector(t *testing.T) {
	// Fixed known-answer vector to lock the canonical encoding:
	// canonical = "3|app|4|user|2|id", sha256, first 16 bytes as hex.
	//
	// Expected value computed independently via .NET SHA256:
	//   sha256("3|app|4|user|2|id") =
	//   2f962cfa3b0960b3087955509ef5271a8bca85d7b88f0cb08dc75e79674f0c99
	//   -> first 32 hex chars prefixed with "sess-".
	const want = "sess-2f962cfa3b0960b3087955509ef5271a"
	if got := SessionWorkspaceKey("app", "user", "id"); got != want {
		t.Errorf("SessionWorkspaceKey(\"app\",\"user\",\"id\") = %q, want %q", got, want)
	}
}
