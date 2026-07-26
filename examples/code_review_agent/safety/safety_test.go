package safety

import (
	"testing"
)

func TestPermissionPolicy_Evaluate(t *testing.T) {
	policy := NewPermissionPolicy()

	tests := []struct {
		name     string
		command  string
		expected string
	}{
		{
			name:     "go vet allowed",
			command:  "go vet ./...",
			expected: "allow",
		},
		{
			name:     "go test allowed",
			command:  "go test -v ./...",
			expected: "allow",
		},
		{
			name:     "curl needs review",
			command:  "curl https://example.com",
			expected: "needs_human_review",
		},
		{
			name:     "rm -rf denied",
			command:  "rm -rf /",
			expected: "deny",
		},
		{
			name:     "sudo denied",
			command:  "sudo apt-get install",
			expected: "deny",
		},
		{
			name:     "unknown command needs review",
			command:  "python script.py",
			expected: "needs_human_review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := policy.Evaluate(tt.command)
			if result.Decision != tt.expected {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.command, result.Decision, tt.expected)
			}
		})
	}
}

func TestSecretDetector_Detect(t *testing.T) {
	detector := NewSecretDetector()

	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{
			name:     "api key",
			text:     `api_key = "sk-1234567890abcdef1234567890abcdef"`,
			expected: true,
		},
		{
			name:     "password",
			text:     `password = "my_secret_password_123"`,
			expected: true,
		},
		{
			name:     "no secrets",
			text:     `fmt.Println("Hello, World!")`,
			expected: false,
		},
		{
			name:     "private key",
			text:     `-----BEGIN RSA PRIVATE KEY-----`,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.Detect(tt.text)
			if result != tt.expected {
				t.Errorf("Detect() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestSecretDetector_RedactText(t *testing.T) {
	detector := NewSecretDetector()

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "api key redacted",
			input:    `api_key = "sk-1234567890abcdef1234567890abcdef"`,
			contains: "<redacted>",
		},
		{
			name:     "normal text unchanged",
			input:    `fmt.Println("Hello")`,
			contains: "fmt.Println",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.RedactText(tt.input)
			if tt.contains == "<redacted>" {
				if result == tt.input {
					t.Error("expected text to be redacted")
				}
			} else {
				if result != tt.input {
					t.Error("expected text to be unchanged")
				}
			}
		})
	}
}
