//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redact

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSensitiveText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"assignment", `API_KEY="secret-value"`, `API_KEY="[REDACTED]"`},
		{"hyphenated assignment", `api-key=secret-value`, `api-key=[REDACTED]`},
		{"colon", `{"access_token":"secret-value"}`, `{"access_token":"[REDACTED]"}`},
		{"hyphenated colon", `{"api-key":"secret-value"}`, `{"api-key":"[REDACTED]"}`},
		{"prefixed hyphenated colon", `{"x-api-key":"secret-value"}`, `{"x-api-key":"[REDACTED]"}`},
		{"flag", `command --password secret-value`, `command --password [REDACTED]`},
		{"authorization header", `Authorization: Bearer secret-value`, `Authorization: Bearer [REDACTED]`},
		{"authorization field", `{"authorization":"secret-value"}`, `{"authorization":"[REDACTED]"}`},
		{"bearer token", `Bearer abcdefghijklmnop`, `Bearer [REDACTED]`},
		{"openai key", `sk-abcdefghijklmnopqrstuvwxyz`, `[REDACTED]`},
		{"jwt", `eyJabcdefghij.abcdefghijkl.abcdefghijkl`, `[REDACTED]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			redacted := SensitiveText(test.input)
			assert.Equal(t, test.want, redacted)
			assert.NotContains(t, redacted, "secret-value")
		})
	}
	assert.Equal(t, "", SensitiveText(""))
	assert.Equal(t, "   ", SensitiveText("   "))
	assert.Equal(t, "ordinary text", SensitiveText("ordinary text"))
}

func TestStructuredValueAndHelpers(t *testing.T) {
	assert.Equal(t, `"`+Value+`"`, StructuredValue(`"secret"`))
	assert.Equal(t, `'`+Value+`'`, StructuredValue(`'secret'`))
	assert.Equal(t, Value, StructuredValue("secret"))
	assert.Equal(t, `"`+Value+`",  `, StructuredValue(`"secret",  `))
	assert.True(t, HasWrappedQuotes(`"value"`, '"'))
	assert.False(t, HasWrappedQuotes(`"`, '"'))
	assert.True(t, IsSensitiveName("MY_API_KEY"))
	assert.True(t, IsSensitiveName("x-api-key"))
	assert.True(t, IsSensitiveName("private-key"))
	assert.False(t, IsSensitiveName("USERNAME"))
}

func TestMatchHelpersPreserveNonMatches(t *testing.T) {
	assert.Equal(t, "ordinary", redactAuthorizationFieldMatch("ordinary"))
	assert.Equal(t, "ordinary=value", redactAssignmentMatch("ordinary=value"))
	assert.Equal(t, `"ordinary":"value"`, redactColonMatch(`"ordinary":"value"`))
}
