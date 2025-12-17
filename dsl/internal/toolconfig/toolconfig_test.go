package toolconfig

import (
	"strings"
	"testing"
)

func TestParseStringSlice(t *testing.T) {
	_, err := ParseStringSlice(nil, "tools")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	got, err := ParseStringSlice([]any{" a ", "", "b"}, "tools")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v, want [a b]", got)
	}

	_, err = ParseStringSlice([]any{"ok", 123}, "tools")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "tools[1]") {
		t.Fatalf("error should mention tools[1], got: %v", err)
	}
}

func TestParseMCPTools(t *testing.T) {
	_, err := ParseMCPTools("bad")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	_, err = ParseMCPTools([]any{"bad"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	_, err = ParseMCPTools([]any{map[string]any{}})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "server_url") {
		t.Fatalf("error should mention server_url, got: %v", err)
	}

	specs, err := ParseMCPTools([]any{
		map[string]any{
			"server_url":    " https://example.invalid/mcp ",
			"allowed_tools": []any{"a", " ", "b"},
			"headers": map[string]any{
				"X-Test": " v ",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(specs))
	}
	if specs[0].ServerURL != "https://example.invalid/mcp" {
		t.Fatalf("server_url=%q", specs[0].ServerURL)
	}
	if specs[0].Transport != MCPTransportStreamableHTTP {
		t.Fatalf("transport=%q, want %q", specs[0].Transport, MCPTransportStreamableHTTP)
	}
	if len(specs[0].AllowedTools) != 2 || specs[0].AllowedTools[0] != "a" || specs[0].AllowedTools[1] != "b" {
		t.Fatalf("allowed_tools=%v, want [a b]", specs[0].AllowedTools)
	}
	if got := specs[0].Headers["X-Test"]; got != "v" {
		t.Fatalf("headers[X-Test]=%q, want %q", got, "v")
	}

	specs, err = ParseMCPTools([]map[string]any{
		{
			"server_url":    "https://example.invalid/mcp",
			"transport":     "sse",
			"allowed_tools": []string{"a", "b"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(specs))
	}
	if specs[0].Transport != MCPTransportSSE {
		t.Fatalf("transport=%q, want %q", specs[0].Transport, MCPTransportSSE)
	}
	if len(specs[0].AllowedTools) != 2 {
		t.Fatalf("allowed_tools=%v", specs[0].AllowedTools)
	}

	_, err = ParseMCPTools([]any{
		map[string]any{
			"server_url": "https://example.invalid/mcp",
			"transport":  "xml",
		},
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	_, err = ParseMCPTools([]any{
		map[string]any{
			"server_url": "https://example.invalid/mcp",
			"headers": map[string]any{
				"X-Bad": 123,
			},
		},
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "headers") {
		t.Fatalf("error should mention headers, got: %v", err)
	}
}
