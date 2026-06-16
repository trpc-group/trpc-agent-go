//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// --- redactSensitiveText additional patterns ---

func TestRedactSensitiveText_OpenAIKey(t *testing.T) {
	text := "use key sk-abcdefghijklmnopqrstuvwxyz012345678 for auth"
	got := redactSensitiveText(text)
	assert.NotContains(t, got, "sk-abcdefghijklmnopqrstuvwxyz012345678")
	assert.Contains(t, got, reviewerRedactedValue)
}

func TestRedactSensitiveText_JWT(t *testing.T) {
	// Build a fake JWT at runtime to avoid tripping secret scanners on the literal.
	jwt := "eyJhbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiIxMjM0NTY3ODkwIn0" + "." + "dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	text := "token: " + jwt
	got := redactSensitiveText(text)
	assert.NotContains(t, got, jwt)
	assert.Contains(t, got, reviewerRedactedValue)
}

func TestRedactSensitiveText_BearerToken(t *testing.T) {
	text := "Authorization: Bearer super-secret-token-value-123456"
	got := redactSensitiveText(text)
	assert.NotContains(t, got, "super-secret-token-value-123456")
	assert.Contains(t, got, reviewerRedactedValue)
}

func TestRedactSensitiveText_AssignmentWithSensitiveName(t *testing.T) {
	text := `MY_SECRET_TOKEN = "very-secret-value-12345678"`
	got := redactSensitiveText(text)
	assert.NotContains(t, got, "very-secret-value-12345678")
	assert.Contains(t, got, reviewerRedactedValue)
}

func TestRedactSensitiveText_ColonPatternSensitiveName(t *testing.T) {
	text := `"api_key": "abcdef1234567890abcdef"`
	got := redactSensitiveText(text)
	assert.NotContains(t, got, "abcdef1234567890abcdef")
	assert.Contains(t, got, reviewerRedactedValue)
}

func TestRedactSensitiveText_FlagPattern(t *testing.T) {
	text := `run --api-key super-secret-key123 --verbose`
	got := redactSensitiveText(text)
	assert.NotContains(t, got, "super-secret-key123")
	assert.Contains(t, got, reviewerRedactedValue)
}

func TestRedactSensitiveText_NoSensitiveContent(t *testing.T) {
	text := "this is a normal line with no secrets"
	got := redactSensitiveText(text)
	assert.Equal(t, text, got)
}

func TestRedactSensitiveText_EmptyString(t *testing.T) {
	assert.Equal(t, "", redactSensitiveText(""))
}

func TestRedactSensitiveText_WhitespaceOnly(t *testing.T) {
	assert.Equal(t, "   ", redactSensitiveText("   "))
}

func TestRedactSensitiveText_AWSAccessKeyID(t *testing.T) {
	text := "aws_secret_access_key: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	got := redactSensitiveText(text)
	assert.NotContains(t, got, "wJalrXUtnFEMI")
	assert.Contains(t, got, reviewerRedactedValue)
}

// --- sanitizeReviewInput ---

func TestSanitizeReviewInput_Nil(t *testing.T) {
	assert.Nil(t, sanitizeReviewInput(nil))
}

func TestSanitizeReviewInput_RedactsMessages(t *testing.T) {
	in := &ReviewInput{
		Messages: []model.Message{{
			Role:    model.RoleUser,
			Content: "set PASSWORD=mysecretpass123456",
		}},
	}
	out := sanitizeReviewInput(in)
	assert.NotContains(t, out.Messages[0].Content, "mysecretpass123456")
	assert.Contains(t, out.Messages[0].Content, reviewerRedactedValue)
}

func TestSanitizeReviewInput_RedactsTranscript(t *testing.T) {
	in := &ReviewInput{
		Transcript: []ReviewMessage{{
			Role:    model.RoleAssistant,
			Content: "API_KEY=sk-secretkey1234567890abcdef in code",
			ToolCalls: []ReviewToolCall{{
				Name:      "exec",
				Arguments: `{"token":"sk-secretkey1234567890abcdef"}`,
			}},
		}},
	}
	out := sanitizeReviewInput(in)
	assert.NotContains(t, out.Transcript[0].Content, "sk-secretkey1234567890abcdef")
	assert.NotContains(t, out.Transcript[0].ToolCalls[0].Arguments, "sk-secretkey1234567890abcdef")
}

func TestSanitizeReviewInput_RedactsExistingSkills(t *testing.T) {
	in := &ReviewInput{
		ExistingSkills: []ExistingSkill{{
			Name:        "deploy",
			Description: "set MY_SECRET=mysupersecretkey123456",
			BodyExcerpt: "use api_key: \"hidden-value-00000000000\"",
		}},
	}
	out := sanitizeReviewInput(in)
	assert.NotContains(t, out.ExistingSkills[0].Description, "mysupersecretkey123456")
	assert.NotContains(t, out.ExistingSkills[0].BodyExcerpt, "hidden-value-00000000000")
}

func TestSanitizeReviewInput_RedactsOutcome(t *testing.T) {
	in := &ReviewInput{
		Outcome: &Outcome{
			Notes: "Authorization: Bearer tok-real-secret-12345",
		},
	}
	out := sanitizeReviewInput(in)
	assert.NotContains(t, out.Outcome.Notes, "tok-real-secret-12345")
	assert.Contains(t, out.Outcome.Notes, reviewerRedactedValue)
}

func TestSanitizeReviewInput_NilOutcomeStaysNil(t *testing.T) {
	in := &ReviewInput{
		Outcome: nil,
	}
	out := sanitizeReviewInput(in)
	assert.Nil(t, out.Outcome)
}

func TestSanitizeModelMessages_ContentParts(t *testing.T) {
	secret := "PASSWORD=mysecretpass1234567890"
	msgs := []model.Message{{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{{
			Text: &secret,
		}},
	}}
	out := sanitizeModelMessages(msgs)
	assert.NotContains(t, *out[0].ContentParts[0].Text, "mysecretpass1234567890")
}

func TestSanitizeModelMessages_ToolCalls(t *testing.T) {
	msgs := []model.Message{{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name:      "exec",
				Arguments: []byte(`{"API_KEY":"abcdefghijklmnopqrstuvwxyz"}`),
			},
		}},
	}}
	out := sanitizeModelMessages(msgs)
	assert.NotContains(t, string(out[0].ToolCalls[0].Function.Arguments), "abcdefghijklmnopqrstuvwxyz")
}

func TestSanitizeModelMessages_ReasoningContent(t *testing.T) {
	msgs := []model.Message{{
		Role:             model.RoleAssistant,
		Content:          "ok",
		ReasoningContent: "api_key = sk-test-REDACT-reasoning-1234567890",
	}}
	out := sanitizeModelMessages(msgs)
	assert.NotContains(t, out[0].ReasoningContent, "sk-test-REDACT-reasoning-1234567890")
}

func TestSanitizeModelMessages_Empty(t *testing.T) {
	assert.Nil(t, sanitizeModelMessages(nil))
	assert.Nil(t, sanitizeModelMessages([]model.Message{}))
}

func TestSanitizeReviewMessages_Empty(t *testing.T) {
	assert.Nil(t, sanitizeReviewMessages(nil))
	assert.Nil(t, sanitizeReviewMessages([]ReviewMessage{}))
}

func TestSanitizeExistingSkills_Empty(t *testing.T) {
	assert.Nil(t, sanitizeExistingSkills(nil))
	assert.Nil(t, sanitizeExistingSkills([]ExistingSkill{}))
}

// --- helpers ---

func TestIsReviewerSensitiveName(t *testing.T) {
	assert.True(t, isReviewerSensitiveName("API_KEY"))
	assert.True(t, isReviewerSensitiveName("MY_SECRET"))
	assert.True(t, isReviewerSensitiveName("DB_PASSWORD"))
	assert.True(t, isReviewerSensitiveName("ACCESS_KEY"))
	assert.True(t, isReviewerSensitiveName("PRIVATE_KEY"))
	assert.True(t, isReviewerSensitiveName("AUTH_TOKEN"))
	assert.False(t, isReviewerSensitiveName("USERNAME"))
	assert.False(t, isReviewerSensitiveName("CONFIG"))
}

func TestHasWrappedQuotes(t *testing.T) {
	assert.True(t, hasWrappedQuotes(`"hello"`, '"'))
	assert.True(t, hasWrappedQuotes(`'hello'`, '\''))
	assert.False(t, hasWrappedQuotes(`hello`, '"'))
	assert.False(t, hasWrappedQuotes(`"`, '"'))
	assert.False(t, hasWrappedQuotes(``, '"'))
}

func TestRedactedStructuredValue_Quoted(t *testing.T) {
	got := redactedStructuredValue(`"secret123"`)
	assert.Equal(t, `"`+reviewerRedactedValue+`"`, got)
}

func TestRedactedStructuredValue_SingleQuoted(t *testing.T) {
	got := redactedStructuredValue(`'secret123'`)
	assert.Equal(t, `'`+reviewerRedactedValue+`'`, got)
}

func TestRedactedStructuredValue_Unquoted(t *testing.T) {
	got := redactedStructuredValue("secret123")
	assert.Equal(t, reviewerRedactedValue, got)
}

func TestRedactedStructuredValue_TrailingComma(t *testing.T) {
	got := redactedStructuredValue(`"secret123",`)
	assert.Equal(t, `"`+reviewerRedactedValue+`",`, got)
}
