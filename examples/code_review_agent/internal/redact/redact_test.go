//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redact

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSanitizerDetectAndMask(t *testing.T) {
	s := New()
	input := []byte("safe=true\napi_key = \"sk-abcdefghijklmnopqrst\"\nAuthorization: Bearer abcdefghijklmnop\n")

	result := s.DetectAndMask(input)
	if got := string(result.Masked); strings.Contains(got, "sk-abcdefghijklmnopqrst") || strings.Contains(got, "abcdefghijklmnop") {
		t.Fatalf("masked content still contains plaintext: %s", got)
	}
	if len(result.Signals) != 2 {
		t.Fatalf("signal count = %d, want 2: %#v", len(result.Signals), result.Signals)
	}
	if result.Signals[0].Line != 2 || result.Signals[1].Line != 3 {
		t.Fatalf("signal lines = %d,%d, want 2,3", result.Signals[0].Line, result.Signals[1].Line)
	}
	for _, signal := range result.Signals {
		if strings.Contains(signal.Evidence, "abcdefghijkl") {
			t.Fatalf("signal evidence contains plaintext: %#v", signal)
		}
		if len(signal.Fingerprint) != 16 {
			t.Fatalf("fingerprint %q is not a short SHA-256 fingerprint", signal.Fingerprint)
		}
	}
}

func TestSanitizerMasksPrivateKeyWithoutChangingLineCount(t *testing.T) {
	s := New()
	input := []byte("const key = `-----BEGIN PRIVATE KEY-----\nabc123\n-----END PRIVATE KEY-----`\n")
	result := s.DetectAndMask(input)
	if len(result.Signals) != 1 {
		t.Fatalf("signal count = %d, want 1", len(result.Signals))
	}
	if strings.Count(string(result.Masked), "\n") != strings.Count(string(input), "\n") {
		t.Fatalf("unexpected masked private key: %q", result.Masked)
	}
}

func TestSanitizerMasksPrivateKeyInUnifiedDiff(t *testing.T) {
	s := New()
	input := []byte("@@ -0,0 +1,3 @@\n+-----BEGIN PRIVATE KEY-----\n+abc123-private-material\n+-----END PRIVATE KEY-----\n")
	result := s.DetectAndMask(input)
	if len(result.Signals) != 1 {
		t.Fatalf("signal count = %d, want 1", len(result.Signals))
	}
	if strings.Contains(string(result.Masked), "abc123-private-material") || strings.Contains(string(result.Masked), "PRIVATE KEY") {
		t.Fatalf("masked diff contains private key material: %q", result.Masked)
	}
	if strings.Count(string(result.Masked), "\n") != strings.Count(string(input), "\n") {
		t.Fatalf("masked diff changed line count: %q", result.Masked)
	}
}

func TestSanitizerDetectsSupportedCredentialFormats(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		plaintext string
		ruleID    string
	}{
		{
			name:      "private key",
			input:     "-----BEGIN PRIVATE KEY-----\nabc123\n-----END PRIVATE KEY-----",
			plaintext: "abc123",
			ruleID:    "SECRET-PRIVATE-KEY",
		},
		{name: "bearer token", input: "Bearer abcdefghijklmnop", plaintext: "abcdefghijklmnop", ruleID: "SECRET-BEARER"},
		{name: "GitHub personal access token", input: "ghp_abcdefghijklmnopqrstuvwxyz123456", plaintext: "ghp_abcdefghijklmnopqrstuvwxyz123456", ruleID: "SECRET-GITHUB"},
		{name: "GitHub OAuth token", input: "gho_abcdefghijklmnopqrstuvwxyz123456", plaintext: "gho_abcdefghijklmnopqrstuvwxyz123456", ruleID: "SECRET-GITHUB"},
		{name: "OpenAI API key", input: "sk-abcdefghijklmnopqrstuvwx", plaintext: "sk-abcdefghijklmnopqrstuvwx", ruleID: "SECRET-OPENAI"},
		{name: "GitLab token", input: "glpat-abcdefghijklmnopqrstuvwxyz1234", plaintext: "glpat-abcdefghijklmnopqrstuvwxyz1234", ruleID: "SECRET-GITLAB"},
		{name: "Slack token", input: "xox" + "b-1234567890-abcdefghijklmnop", plaintext: "xox" + "b-1234567890-abcdefghijklmnop", ruleID: "SECRET-SLACK"},
		{name: "Google API key", input: "AIzaabcdefghijklmnopqrstuvwxyz123456", plaintext: "AIzaabcdefghijklmnopqrstuvwxyz123456", ruleID: "SECRET-GOOGLE-API-KEY"},
		{name: "Stripe key", input: "sk_li" + "ve_abcdefghijklmnopqrstuvwx", plaintext: "sk_li" + "ve_abcdefghijklmnopqrstuvwx", ruleID: "SECRET-STRIPE"},
		{name: "SendGrid key", input: "SG.abcdefghijklmnopqrstu.abcdefghijklmnopqrstu", plaintext: "SG.abcdefghijklmnopqrstu.abcdefghijklmnopqrstu", ruleID: "SECRET-SENDGRID"},
		{name: "npm token", input: "npm_abcdefghijklmnopqrstuvwxyz123456", plaintext: "npm_abcdefghijklmnopqrstuvwxyz123456", ruleID: "SECRET-NPM"},
		{name: "Twilio key", input: "S" + "K0123456789abcdef0123456789abcdef", plaintext: "S" + "K0123456789abcdef0123456789abcdef", ruleID: "SECRET-TWILIO"},
		{name: "AWS access key", input: "AKI" + "AABCDEFGHIJKLMNOP", plaintext: "AKI" + "AABCDEFGHIJKLMNOP", ruleID: "SECRET-AWS-ACCESS-KEY"},
		{name: "JWT", input: "eyJabcdef.abcdefgh.ijklmnop", plaintext: "eyJabcdef.abcdefgh.ijklmnop", ruleID: "SECRET-JWT"},
		{name: "HTTP URL password", input: "https://user:http-password@example.com", plaintext: "http-password", ruleID: "SECRET-URL-USERINFO"},
		{name: "PostgreSQL URL password", input: "postgres://user:database-password@db.example/app", plaintext: "database-password", ruleID: "SECRET-URL-USERINFO"},
		{name: "MongoDB URL password", input: "mongodb+srv://user:mongo-password@db.example/app", plaintext: "mongo-password", ruleID: "SECRET-URL-USERINFO"},
		{name: "password assignment", input: "password=assignment-secret", plaintext: "assignment-secret", ruleID: "SECRET-ASSIGNMENT"},
		{name: "AWS secret assignment", input: "AWS_SECRET_ACCESS_KEY=aws-secret-material", plaintext: "aws-secret-material", ruleID: "SECRET-ASSIGNMENT"},
		{name: "client secret assignment", input: "client_secret=client-secret-material", plaintext: "client-secret-material", ruleID: "SECRET-ASSIGNMENT"},
	}
	sanitizer := New()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := sanitizer.DetectAndMask([]byte(test.input))
			if strings.Contains(string(result.Masked), test.plaintext) {
				t.Fatalf("masked content contains plaintext %q: %q", test.plaintext, result.Masked)
			}
			for _, signal := range result.Signals {
				if signal.RuleID == test.ruleID {
					return
				}
			}
			t.Fatalf("signals = %#v, want rule %q", result.Signals, test.ruleID)
		})
	}
}

func TestSanitizerMaskValuePreservesJSONShape(t *testing.T) {
	s := New()
	masked, count, err := s.MaskValue(map[string]any{
		"status": "ok",
		"output": "password=hunter2-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	data, _ := json.Marshal(masked)
	if strings.Contains(string(data), "hunter2-secret") {
		t.Fatalf("masked JSON contains plaintext: %s", data)
	}
}

func TestAppendEventHookMasksBeforeNext(t *testing.T) {
	s := New()
	evt := event.NewResponseEvent("inv", "user", &model.Response{
		Choices: []model.Choice{{Message: model.NewUserMessage("token=super-secret-token")}},
	})
	called := false
	hook := AppendEventHook(s)
	err := hook(&session.AppendEventContext{
		Context: context.Background(),
		Event:   evt,
	}, func() error {
		called = true
		data, _ := json.Marshal(evt)
		if strings.Contains(string(data), "super-secret-token") {
			t.Fatalf("event reached next with plaintext: %s", data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("hook did not call next")
	}
}

func TestMaskEventHandlesJSONEscapedMultilineSecret(t *testing.T) {
	s := New()
	secret := "-----BEGIN PRIVATE KEY-----\nabc123\n-----END PRIVATE KEY-----"
	evt := event.NewResponseEvent("inv", "user", &model.Response{
		Choices: []model.Choice{{Message: model.NewUserMessage(secret)}},
	})
	count, err := s.MaskEvent(evt)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	data, _ := json.Marshal(evt)
	if strings.Contains(string(data), "abc123") {
		t.Fatalf("event contains private key material: %s", data)
	}
}

func TestMaskEventPreservesRuntimeFieldsAndMasksStateDelta(t *testing.T) {
	s := New()
	runtimeValue := &struct{ Name string }{Name: "typed output"}
	evt := event.NewResponseEvent("inv", "user", &model.Response{
		Choices: []model.Choice{{Message: model.NewUserMessage("safe")}},
	})
	evt.StructuredOutput = runtimeValue
	evt.StateDelta = map[string][]byte{"credential": []byte("password=state-secret-value")}

	count, err := s.MaskEvent(evt)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if evt.StructuredOutput != runtimeValue {
		t.Fatal("MaskEvent discarded the runtime-only StructuredOutput")
	}
	if strings.Contains(string(evt.StateDelta["credential"]), "state-secret-value") {
		t.Fatalf("StateDelta contains plaintext: %q", evt.StateDelta["credential"])
	}
}
