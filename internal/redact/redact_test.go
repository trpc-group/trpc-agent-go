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
	}{
		{"assignment", `API_KEY="secret-value"`},
		{"colon", `{"access_token":"secret-value"}`},
		{"flag", `command --password secret-value`},
		{"authorization header", `Authorization: Bearer secret-value`},
		{"authorization field", `{"authorization":"secret-value"}`},
		{"bearer token", `Bearer abcdefghijklmnop`},
		{"openai key", `sk-abcdefghijklmnopqrstuvwxyz`},
		{"jwt", `eyJabcdefghij.abcdefghijkl.abcdefghijkl`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			redacted := SensitiveText(test.input)
			assert.Contains(t, redacted, Value)
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
	assert.False(t, IsSensitiveName("USERNAME"))
}

func TestMatchHelpersPreserveNonMatches(t *testing.T) {
	assert.Equal(t, "ordinary", redactAuthorizationFieldMatch("ordinary"))
	assert.Equal(t, "ordinary=value", redactAssignmentMatch("ordinary=value"))
	assert.Equal(t, `"ordinary":"value"`, redactColonMatch(`"ordinary":"value"`))
}
