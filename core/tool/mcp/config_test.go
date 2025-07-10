package mcp

import "testing"

// TestValidateTransport covers accepted and rejected transport strings.
func TestValidateTransport(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantTransport transport
		wantErr       bool
	}{
		{name: "stdio", input: "stdio", wantTransport: transportStdio},
		{name: "sse", input: "sse", wantTransport: transportSSE},
		{name: "streamable", input: "streamable", wantTransport: transportStreamable},
		{name: "streamable_http", input: "streamable_http", wantTransport: transportStreamable},
		{name: "invalid", input: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateTransport(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %s", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantTransport {
				t.Fatalf("got transport %s want %s", got, tt.wantTransport)
			}
		})
	}
}
