package redaction

import (
	"testing"
)

func TestRedactSecrets_APIKey(t *testing.T) {
	testCases := []struct {
		input    string
		expected bool
	}{
		{"APIKey = \"sk-1234567890abcdef1234567890abcdef\"", true},
		{"api_key = \"my-secret-key\"", true},
		{"secret_key = \"abc123\"", true},
		{"access_token = \"token123\"", true},
		{"password = \"admin123\"", true},
		{"normal text", false},
	}

	for _, tc := range testCases {
		result := ContainsSecret(tc.input)
		if result != tc.expected {
			t.Errorf("ContainsSecret(%q) = %v, expected %v", tc.input, result, tc.expected)
		}

		redacted := RedactSecrets(tc.input)
		if tc.expected && redacted == tc.input {
			t.Errorf("Expected secrets to be redacted in %q", tc.input)
		}
	}
}

func TestRedactSecrets_JWT(t *testing.T) {
	input := "Token = \"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c\""
	redacted := RedactSecrets(input)
	if redacted == input {
		t.Error("Expected JWT token to be redacted")
	}
	if len(redacted) > len(input) {
		t.Error("Redacted output should not be longer than input")
	}
}

func TestRedactSecrets_AWSKeys(t *testing.T) {
	testCases := []string{
		"AWS_ACCESS_KEY_ID = \"AKIAIOSFODNN7EXAMPLE\"",
		"AWS_SECRET_ACCESS_KEY = \"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\"",
	}

	for _, tc := range testCases {
		redacted := RedactSecrets(tc)
		if redacted == tc {
			t.Errorf("Expected AWS key to be redacted: %s", tc)
		}
	}
}

func TestRedactFindingContent(t *testing.T) {
	input := "Found secret: APIKey = \"sk-1234567890abcdef\""
	redacted := RedactFindingContent(input)
	if redacted == input {
		t.Error("Expected finding content to be redacted")
	}
}

func TestRedactDiffCode(t *testing.T) {
	input := "+    APIKey = \"sk-1234567890abcdef1234567890abcdef\""
	redacted := RedactDiffCode(input)
	if redacted == input {
		t.Error("Expected diff code to be redacted")
	}
}

func TestRedactAllStrings(t *testing.T) {
	input := []string{
		"APIKey = \"sk-1234567890abcdef\"",
		"normal text",
		"password = \"secret123\"",
	}
	redacted := RedactAllStrings(input)
	if len(redacted) != len(input) {
		t.Errorf("Expected %d strings, got %d", len(input), len(redacted))
	}
	if redacted[0] == input[0] {
		t.Error("Expected first string to be redacted")
	}
	if redacted[1] != input[1] {
		t.Error("Expected second string to remain unchanged")
	}
	if redacted[2] == input[2] {
		t.Error("Expected third string to be redacted")
	}
}
