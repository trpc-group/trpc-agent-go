//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestParsePolicyStrictJSONAndYAML(t *testing.T) {
	tests := []struct {
		name, format, input string
		wantErr             bool
	}{
		{"json", PolicyFormatJSON, `{"version":1,"profiles":{"web":{"allowed_domains":["example.com","*.example.org"]}}}`, false},
		{"yaml", PolicyFormatYAML, "version: 1\ndefault_action: allow\nprofiles:\n  web:\n    allowed_domains: [example.com]\n", false},
		{"unknown JSON", PolicyFormatJSON, `{"version":1,"unknown":true}`, true},
		{"unknown YAML", PolicyFormatYAML, "version: 1\nunknown: true\n", true},
		{"duplicate YAML", PolicyFormatYAML, "version: 1\nversion: 1\n", true},
		{"trailing JSON", PolicyFormatJSON, `{"version":1}{"version":1}`, true},
		{"duplicate JSON", PolicyFormatJSON, `{"version":1,"profiles":{"x":{}},"profiles":{}}`, true},
		{"multiple YAML documents", PolicyFormatYAML, "version: 1\n---\nversion: 1\n", true},
		{"implicit wildcard", PolicyFormatJSON, `{"version":1,"profiles":{"web":{"allowed_domains":["*example.com"]}}}`, true},
		{"URL as domain", PolicyFormatJSON, `{"version":1,"profiles":{"web":{"allowed_domains":["https://example.com"]}}}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePolicy([]byte(tc.input), tc.format)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParsePolicy() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidatePolicySecretRuleCannotBeDisabled(t *testing.T) {
	disabled := false
	policy := DefaultPolicy()
	policy.Rules.SecretExposure.Enabled = &disabled
	if err := ValidatePolicy(policy); err == nil {
		t.Fatal("expected disabling secret rule to fail")
	}
	policy = DefaultPolicy()
	policy.Rules.SecretExposure.Action = tool.PermissionActionAllow
	if err := ValidatePolicy(policy); err == nil {
		t.Fatal("expected allowing secret exposure to fail")
	}
}

func TestDurationPolicyRoundTrip(t *testing.T) {
	policy, err := ParsePolicy([]byte(`{"version":1,"profiles":{"exec":{"max_timeout":"30s","max_output_bytes":4096}}}`), PolicyFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Profiles["exec"].MaxTimeout != Duration(30*time.Second) {
		t.Fatalf("max timeout = %v", policy.Profiles["exec"].MaxTimeout)
	}
	data, err := json.Marshal(policy.Profiles["exec"].MaxTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"30s"` {
		t.Fatalf("duration JSON = %s", data)
	}
	policy, err = ParsePolicy([]byte("version: 1\nprofiles:\n  exec:\n    max_timeout: 45s\n"), PolicyFormatYAML)
	if err != nil || policy.Profiles["exec"].MaxTimeout != Duration(45*time.Second) {
		t.Fatalf("YAML duration: policy=%+v err=%v", policy, err)
	}
}

func TestDomainAllowedExactAndExplicitWildcard(t *testing.T) {
	patterns := []string{"example.com", "*.trusted.example", "192.0.2.1"}
	tests := []struct {
		host string
		want bool
	}{
		{"example.com", true},
		{"sub.example.com", false},
		{"trusted.example", false},
		{"a.trusted.example", true},
		{"deep.a.trusted.example", true},
		{"eviltrusted.example", false},
		{"192.0.2.1", true},
	}
	for _, tc := range tests {
		if got := domainAllowed(tc.host, patterns); got != tc.want {
			t.Errorf("domainAllowed(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
