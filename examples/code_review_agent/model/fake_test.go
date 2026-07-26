package model

import (
	"context"
	"strings"
	"testing"
)

func TestFakeModel_GenerateResponse(t *testing.T) {
	model := NewFakeModel("fake-gpt")

	tests := []struct {
		name     string
		prompt   string
		contains string
	}{
		{
			name:     "SQL injection detection",
			prompt:   "Review this code: query := fmt.Sprintf(\"SELECT * FROM users WHERE name = '%s'\", username)",
			contains: "SQL Injection",
		},
		{
			name:     "Resource leak detection",
			prompt:   "Review this code: f, err := os.Open(\"file.txt\")",
			contains: "Resource Leak",
		},
		{
			name:     "Goroutine leak detection",
			prompt:   "Review this code: go func() { for { } }",
			contains: "Goroutine Leak",
		},
		{
			name:     "Sensitive info detection",
			prompt:   "Review this code: api_key = \"sk-1234567890abcdef\"",
			contains: "Sensitive Information",
		},
		{
			name:     "No issues",
			prompt:   "Review this code: fmt.Println(\"Hello\")",
			contains: "No issues found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			response, err := model.GenerateResponse(ctx, tt.prompt)
			if err != nil {
				t.Fatalf("GenerateResponse() error = %v", err)
			}

			if !strings.Contains(response, tt.contains) {
				t.Errorf("GenerateResponse() = %s, want contains %s", response, tt.contains)
			}
		})
	}
}

func TestFakeModel_DeterministicOutput(t *testing.T) {
	model := NewFakeModel("fake-gpt")
	ctx := context.Background()
	prompt := "Review this code: query := fmt.Sprintf(\"SELECT * FROM users WHERE name = '%s'\", username)"

	// 多次调用应该返回相同结果
	response1, _ := model.GenerateResponse(ctx, prompt)
	response2, _ := model.GenerateResponse(ctx, prompt)

	if response1 != response2 {
		t.Error("FakeModel should return deterministic output")
	}
}
