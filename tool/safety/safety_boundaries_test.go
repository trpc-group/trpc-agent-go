//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type unmarshalableRedactionValue struct {
	Callback func()
	secret   string
}

func (v unmarshalableRedactionValue) String() string {
	return v.secret
}

func TestRedactValueBoundaryInputs(t *testing.T) {
	tests := []struct {
		name        string
		value       any
		want        any
		wantChanged bool
		wantError   bool
	}{
		{name: "nil", value: nil, want: nil},
		{name: "safe string", value: "safe", want: "safe"},
		{
			name:        "secret string",
			value:       "api_key=abcdefghijklmnop",
			want:        "api_key=[REDACTED]",
			wantChanged: true,
		},
		{
			name: "unmarshalable safe value",
			value: unmarshalableRedactionValue{
				Callback: func() {},
				secret:   "safe",
			},
			wantError: true,
		},
		{
			name: "unmarshalable secret value",
			value: unmarshalableRedactionValue{
				Callback: func() {},
				secret:   "password=abcdefghijklmnop",
			},
			want:        "password=[REDACTED]",
			wantChanged: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, changed, err := RedactValue(tc.value)
			if (err != nil) != tc.wantError {
				t.Fatalf("RedactValue() error = %v, wantError %v", err, tc.wantError)
			}
			if changed != tc.wantChanged {
				t.Errorf("RedactValue() changed = %v, want %v", changed, tc.wantChanged)
			}
			if !tc.wantError && got != tc.want {
				t.Errorf("RedactValue() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestRedactingAfterToolCallbackBoundaryInputs(t *testing.T) {
	callback := NewRedactingAfterToolCallback()

	for _, args := range []*tool.AfterToolArgs{
		nil,
		{Result: "safe"},
	} {
		result, err := callback(context.Background(), args)
		if err != nil {
			t.Fatalf("callback() error = %v", err)
		}
		if result == nil || result.CustomResult != nil {
			t.Fatalf("callback() = %+v, want unchanged result", result)
		}
	}
	result, err := callback(context.Background(), &tool.AfterToolArgs{
		Result: "api_key=abcdefghijklmnop",
	})
	if err != nil {
		t.Fatalf("callback(redacted) error = %v", err)
	}
	if result == nil || result.CustomResult != "api_key=[REDACTED]" {
		t.Fatalf(
			"callback(redacted) = %+v, want redacted custom result",
			result,
		)
	}

	_, err = callback(context.Background(), &tool.AfterToolArgs{
		Result: unmarshalableRedactionValue{
			Callback: func() {},
			secret:   "safe",
		},
	})
	if err == nil {
		t.Fatal("callback() succeeded for an unmarshalable safe result")
	}
}

func TestJSONLAuditorBoundaryFailures(t *testing.T) {
	var nilAuditor *JSONLAuditor
	if err := nilAuditor.Record(AuditEvent{}); err != nil {
		t.Fatalf("nil auditor Record() error = %v", err)
	}
	if err := NewJSONLAuditor("").Record(AuditEvent{}); err != nil {
		t.Fatalf("empty path Record() error = %v", err)
	}

	directoryPath := t.TempDir()
	if err := NewJSONLAuditor(directoryPath).Record(AuditEvent{}); err == nil {
		t.Fatal("Record() succeeded with a directory path")
	} else if !strings.Contains(err.Error(), "open tool safety audit file") {
		t.Fatalf("Record() error = %q, want open context", err)
	}
}

func TestValidateHostBoundaries(t *testing.T) {
	longLabel := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{name: "domain", host: "github.com"},
		{
			name: "maximum label",
			host: strings.Repeat("a", 63) + ".com",
		},
		{name: "domain with trailing dot", host: "github.com."},
		{name: "IPv4", host: "127.0.0.1"},
		{name: "IPv6", host: "::1"},
		{name: "empty", host: " ", wantErr: true},
		{name: "URL", host: "https://github.com", wantErr: true},
		{name: "empty label", host: "github..com", wantErr: true},
		{name: "long label", host: longLabel + ".com", wantErr: true},
		{name: "leading hyphen", host: "-git.example", wantErr: true},
		{name: "trailing hyphen", host: "git-.example", wantErr: true},
		{name: "invalid character", host: "git_example.com", wantErr: true},
		{name: "wildcard", host: "*.example.com", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateHost(tc.host)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateHost(%q) error = %v, wantErr %v", tc.host, err, tc.wantErr)
			}
		})
	}
}

func TestNewPermissionPolicyUsesDefaultScanner(t *testing.T) {
	policy := NewPermissionPolicy(nil)
	decision, err := policy.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName:  "workspace_exec",
			Arguments: []byte(`{"command":"go test ./tool/safety"}`),
		},
	)
	if err != nil {
		t.Fatalf("CheckToolPermission() error = %v", err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("decision = %+v, want allow", decision)
	}
}

func TestPermissionPolicyDeniesArgumentFailuresWhenAuditFails(
	t *testing.T,
) {
	policy := NewPermissionPolicy(NewScanner(
		DefaultPolicy(),
		WithAuditor(failingAuditor{}),
	))
	tests := []struct {
		name string
		req  *tool.PermissionRequest
	}{
		{name: "nil request"},
		{
			name: "invalid arguments",
			req: &tool.PermissionRequest{
				ToolName:  "workspace_exec",
				Arguments: []byte(`{"command":`),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := policy.CheckToolPermission(
				context.Background(),
				tc.req,
			)
			if err != nil {
				t.Fatalf("CheckToolPermission() error = %v", err)
			}
			if decision.Action != tool.PermissionActionDeny {
				t.Fatalf("decision = %+v, want deny", decision)
			}
			if !strings.Contains(decision.Reason, "audit unavailable") {
				t.Fatalf(
					"decision reason = %q, want audit failure",
					decision.Reason,
				)
			}
		})
	}
}
