package modelspec

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	_, err := Parse(nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	_, err = Parse("bad")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	_, err = Parse(map[string]any{
		"provider":   "openai",
		"model_name": "dummy",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("error should mention api_key, got: %v", err)
	}

	spec, err := Parse(map[string]any{
		"provider":   " openai ",
		"model_name": " dummy ",
		"api_key":    " secret ",
		"base_url":   " https://example.invalid ",
		"headers": map[string]any{
			"X-Test": " v ",
		},
		"extra_fields": map[string]any{
			"foo": "bar",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Provider != "openai" {
		t.Fatalf("provider=%q, want %q", spec.Provider, "openai")
	}
	if spec.ModelName != "dummy" {
		t.Fatalf("model_name=%q, want %q", spec.ModelName, "dummy")
	}
	if spec.APIKey != "secret" {
		t.Fatalf("api_key=%q, want %q", spec.APIKey, "secret")
	}
	if spec.BaseURL != "https://example.invalid" {
		t.Fatalf("base_url=%q, want %q", spec.BaseURL, "https://example.invalid")
	}
	if got := spec.Headers["X-Test"]; got != "v" {
		t.Fatalf("headers[X-Test]=%q, want %q", got, "v")
	}
	if got := spec.ExtraFields["foo"]; got != "bar" {
		t.Fatalf("extra_fields.foo=%v, want %v", got, "bar")
	}
}

func TestResolveEnv(t *testing.T) {
	t.Setenv("DSL_TEST_KEY", "secret")
	t.Setenv("DSL_TEST_BASE_URL", "https://example.invalid")
	t.Setenv("DSL_TEST_HEADER", "header-val")

	_, err := ResolveEnv(Spec{APIKey: "env:DSL_TEST_KEY"}, false)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	resolved, err := ResolveEnv(Spec{
		APIKey:  "env:DSL_TEST_KEY",
		BaseURL: "env:DSL_TEST_BASE_URL",
		Headers: map[string]string{
			"X-Test": "env:DSL_TEST_HEADER",
		},
	}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.APIKey != "secret" {
		t.Fatalf("api_key=%q, want %q", resolved.APIKey, "secret")
	}
	if resolved.BaseURL != "https://example.invalid" {
		t.Fatalf("base_url=%q, want %q", resolved.BaseURL, "https://example.invalid")
	}
	if got := resolved.Headers["X-Test"]; got != "header-val" {
		t.Fatalf("headers[X-Test]=%q, want %q", got, "header-val")
	}

	_, err = ResolveEnv(Spec{APIKey: "env:"}, true)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	t.Setenv("DSL_TEST_EMPTY", "")
	_, err = ResolveEnv(Spec{APIKey: "env:DSL_TEST_EMPTY"}, true)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestNewModel(t *testing.T) {
	_, _, err := NewModel(Spec{Provider: "not-supported"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	m, name, err := NewModel(Spec{
		Provider:  "openai",
		ModelName: "dummy",
		APIKey:    "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatalf("expected model, got nil")
	}
	if name != "dummy" {
		t.Fatalf("name=%q, want %q", name, "dummy")
	}
}

func TestResolveEnv_DoesNotMutateInput(t *testing.T) {
	t.Setenv("DSL_TEST_KEY", "secret")

	orig := Spec{
		APIKey: "env:DSL_TEST_KEY",
		Headers: map[string]string{
			"X-Test": "env:DSL_TEST_KEY",
		},
	}

	_, err := ResolveEnv(orig, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if orig.APIKey != "env:DSL_TEST_KEY" {
		t.Fatalf("orig APIKey mutated: %q", orig.APIKey)
	}
	if got := orig.Headers["X-Test"]; got != "env:DSL_TEST_KEY" {
		t.Fatalf("orig Headers mutated: %q", got)
	}

}
