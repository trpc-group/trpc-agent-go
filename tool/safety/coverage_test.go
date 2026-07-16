//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPolicyStrictParsingAdditionalBranches(t *testing.T) {
	tests := []struct {
		name   string
		format string
		input  string
	}{
		{name: "empty", format: PolicyFormatAuto, input: " \n\t"},
		{name: "unsupported format", format: "toml", input: "version = 1"},
		{name: "malformed JSON", format: PolicyFormatJSON, input: `{"version":1`},
		{name: "malformed nested JSON", format: PolicyFormatJSON, input: `{"version":1,"profiles":[}`},
		{name: "malformed YAML", format: PolicyFormatYAML, input: "version: ["},
		{name: "malformed trailing YAML", format: PolicyFormatYAML, input: "version: 1\n---\n["},
		{name: "numeric JSON duration", format: PolicyFormatJSON, input: `{"version":1,"profiles":{"exec":{"max_timeout":10}}}`},
		{name: "invalid JSON duration", format: PolicyFormatJSON, input: `{"version":1,"profiles":{"exec":{"max_timeout":"forever"}}}`},
		{name: "invalid YAML duration", format: PolicyFormatYAML, input: "version: 1\nprofiles:\n  exec:\n    max_timeout: forever\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParsePolicy([]byte(tc.input), tc.format); err == nil {
				t.Fatal("unsafe or malformed policy was accepted")
			}
		})
	}

	for _, tc := range []struct {
		name, format, input string
	}{
		{"auto JSON", " AUTO ", `{"version":1}`},
		{"auto YAML", "", "version: 1\n"},
		{"YML alias", "YML", "version: 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ParsePolicy([]byte(tc.input), tc.format)
			if err != nil {
				t.Fatalf("valid policy rejected: %v", err)
			}
			if p.Version != CurrentPolicyVersion || p.DefaultAction != tool.PermissionActionAllow {
				t.Fatalf("unexpected defaults: %+v", p)
			}
		})
	}
}

func TestPolicyJSONWalkerRejectsMalformedStructures(t *testing.T) {
	for _, input := range []string{
		`{"version":1,"profiles":{"x":{"allowed_domains":["a.example","a.example"]}}`,
		`{"version":1,"profiles":{"x":{"allowed_domains":["a.example",]}}}`,
		`[1,2`,
	} {
		if err := validateJSONNoDuplicateKeys([]byte(input)); err == nil {
			t.Fatalf("malformed JSON accepted: %q", input)
		}
	}

	dec := json.NewDecoder(strings.NewReader(`[]`))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	if err := walkJSONValue(dec); err == nil || !strings.Contains(err.Error(), "unexpected JSON delimiter") {
		t.Fatalf("unexpected closing delimiter error = %v", err)
	}
	dec = json.NewDecoder(strings.NewReader(`{"version":1} trailing`))
	var firstValue any
	if err := dec.Decode(&firstValue); err != nil {
		t.Fatal(err)
	}
	if err := rejectTrailingJSON(dec); err == nil || !strings.Contains(err.Error(), "decode trailing JSON") {
		t.Fatalf("malformed trailing value error = %v", err)
	}
}

func TestValidatePolicyAdditionalInvalidValues(t *testing.T) {
	invalidAction := tool.PermissionAction("execute")
	negative := Duration(-time.Second)
	tests := []struct {
		name   string
		policy Policy
	}{
		{"version", Policy{Version: CurrentPolicyVersion + 1}},
		{"default action", Policy{Version: CurrentPolicyVersion, DefaultAction: invalidAction}},
		{"rule action", Policy{Version: CurrentPolicyVersion, Rules: BuiltinRules{NetworkAccess: RulePolicy{Action: invalidAction}}}},
		{"empty profile name", Policy{Version: CurrentPolicyVersion, Profiles: map[string]ToolProfile{"  ": {}}}},
		{"upper-case domain", policyWithDomain("Example.com")},
		{"empty domain", policyWithDomain(" ")},
		{"domain port", policyWithDomain("example.com:443")},
		{"empty wildcard", policyWithDomain("*.")},
		{"nested wildcard", policyWithDomain("*.foo.*.example")},
		{"embedded wildcard", policyWithDomain("foo*.example")},
		{"empty domain label", policyWithDomain("foo..example")},
		{"leading hyphen", policyWithDomain("-foo.example")},
		{"trailing hyphen", policyWithDomain("foo-.example")},
		{"invalid domain character", policyWithDomain("foo_bar.example")},
		{"blank allowed command", Policy{Version: CurrentPolicyVersion, Profiles: map[string]ToolProfile{"exec": {AllowedCommands: []string{" "}}}}},
		{"blank denied command", Policy{Version: CurrentPolicyVersion, Profiles: map[string]ToolProfile{"exec": {DeniedCommands: []string{""}}}}},
		{"blank forbidden path", Policy{Version: CurrentPolicyVersion, Profiles: map[string]ToolProfile{"exec": {ForbiddenPaths: []string{"\t"}}}}},
		{"blank allowed environment", Policy{Version: CurrentPolicyVersion, Profiles: map[string]ToolProfile{"exec": {AllowedEnv: []string{""}}}}},
		{"negative timeout", Policy{Version: CurrentPolicyVersion, Profiles: map[string]ToolProfile{"exec": {MaxTimeout: negative}}}},
		{"negative output", Policy{Version: CurrentPolicyVersion, Profiles: map[string]ToolProfile{"exec": {MaxOutputBytes: -1}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePolicy(tc.policy); err == nil {
				t.Fatalf("invalid policy accepted: %+v", tc.policy)
			}
		})
	}

	for _, domain := range []string{"127.0.0.1", "2001:db8::1", "foo-bar.example", "*.sub.example"} {
		if err := validateDomainPattern(domain); err != nil {
			t.Errorf("valid domain pattern %q rejected: %v", domain, err)
		}
	}
}

func policyWithDomain(domain string) Policy {
	return Policy{
		Version:  CurrentPolicyVersion,
		Profiles: map[string]ToolProfile{"net": {AllowedDomains: []string{domain}}},
	}
}

func TestDurationRejectsInvalidRepresentations(t *testing.T) {
	var d Duration
	if err := d.UnmarshalJSON([]byte(`5`)); err == nil {
		t.Fatal("numeric duration should be rejected because its unit is ambiguous")
	}
	if err := d.UnmarshalJSON([]byte(`"later"`)); err == nil {
		t.Fatal("invalid duration string was accepted")
	}
	if err := d.UnmarshalText([]byte("later")); err == nil {
		t.Fatal("invalid YAML duration was accepted")
	}
	if err := d.UnmarshalText([]byte("1500ms")); err != nil || time.Duration(d) != 1500*time.Millisecond {
		t.Fatalf("valid text duration: value=%v err=%v", d, err)
	}
}

func TestPermissionArgumentNormalizationAllSupportedShapes(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName:   "exec",
		ToolCallID: "call-1",
		Arguments: []byte(`{
				"cmd":"echo",
				"argv":["echo","2",""],
			"working_directory":"work",
				"environment":{"PATH":"bin","COUNT":"3","EMPTY":""},
			"input":"hello",
			"source":"print(1)",
			"lang":"python",
			"executor":"sandbox",
			"use_pty":true,
			"run_in_background":true,
			"timeout":"2s",
				"output_limit":4096
		}`),
	}
	got, err := scanRequestFromPermission(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.ToolName != "exec" || got.ToolCallID != "call-1" || got.Command != "echo" {
		t.Fatalf("identity/command normalization failed: %+v", got)
	}
	if !reflect.DeepEqual(got.Args, []string{"echo", "2", ""}) {
		t.Fatalf("argv = %#v", got.Args)
	}
	if got.WorkingDir != "work" || got.Stdin != "hello" || got.Code != "print(1)" || got.Language != "python" || got.Backend != "sandbox" {
		t.Fatalf("string aliases were not normalized: %+v", got)
	}
	if !reflect.DeepEqual(got.Env, map[string]string{"PATH": "bin", "COUNT": "3", "EMPTY": ""}) {
		t.Fatalf("environment = %#v", got.Env)
	}
	if !got.PTY || !got.Background || got.Timeout != 2*time.Second || got.MaxOutputBytes != 4096 {
		t.Fatalf("execution options were not normalized: %+v", got)
	}
	if len(got.RawFields) == 0 {
		t.Fatal("raw fields must be retained for recursive scanning")
	}

	for _, tc := range []struct {
		name string
		args string
		want time.Duration
	}{
		{"numeric timeout uses schema seconds", `{"timeout":2}`, 2 * time.Second},
		{"timeout_ms alias", `{"timeout_ms":2500}`, 2500 * time.Millisecond},
		{"timeout seconds alias", `{"timeout_seconds":3}`, 3 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			normalized, err := scanRequestFromPermission(&tool.PermissionRequest{Arguments: []byte(tc.args)})
			if err != nil || normalized.Timeout != tc.want {
				t.Fatalf("timeout=%v want=%v err=%v", normalized.Timeout, tc.want, err)
			}
		})
	}
}

func TestPermissionArgumentNormalizationRejectsAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []byte
	}{
		{"duplicate key", []byte(`{"command":"echo","command":"rm"}`)},
		{"null object", []byte(`null`)},
		{"array instead of object", []byte(`[]`)},
		{"malformed", []byte(`{"command":`)},
		{"trailing value", []byte(`{"command":"echo"} {}`)},
		{"conflicting aliases", []byte(`{"timeout_sec":1,"timeout_seconds":2}`)},
		{"case-folded alias", []byte(`{"command":"echo","COMMAND":"rm"}`)},
		{"invalid string type", []byte(`{"command":7}`)},
		{"invalid list item", []byte(`{"args":["echo",2]}`)},
		{"invalid environment value", []byte(`{"env":{"COUNT":3}}`)},
		{"invalid boolean", []byte(`{"pty":"true"}`)},
		{"invalid duration", []byte(`{"timeout":"never"}`)},
		{"overflow duration", []byte(`{"timeout_sec":9223372036854775807}`)},
		{"string output limit", []byte(`{"max_output_bytes":"10"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := scanRequestFromPermission(&tool.PermissionRequest{Arguments: tc.args}); err == nil {
				t.Fatal("ambiguous permission payload was accepted")
			}
		})
	}
	got, err := scanRequestFromPermission(&tool.PermissionRequest{ToolName: "poll", Arguments: []byte(" \n")})
	if err != nil || got.ToolName != "poll" || got.RawFields != nil {
		t.Fatalf("empty payload should normalize to an empty request: %+v err=%v", got, err)
	}
}

func TestGuardConstructionNilAndDefensiveCopies(t *testing.T) {
	if _, err := NewGuard(Policy{}); err == nil {
		t.Fatal("invalid policy should prevent guard construction")
	}
	if _, err := NewDefaultGuard(WithAuditSink(nil)); err == nil {
		t.Fatal("nil audit sink was accepted")
	}
	if _, err := NewDefaultGuard(WithPermissionPolicy(nil)); err == nil {
		t.Fatal("nil composed policy was accepted")
	}
	guard, err := NewDefaultGuard(nil)
	if err != nil {
		t.Fatal(err)
	}
	if guard.Policy().Version != CurrentPolicyVersion {
		t.Fatalf("unexpected policy: %+v", guard.Policy())
	}

	policy := DefaultPolicy()
	policy.Profiles = map[string]ToolProfile{"exec": {
		AllowedDomains:  []string{"example.com"},
		DeniedCommands:  []string{"rm"},
		AllowedCommands: []string{"go"},
		ForbiddenPaths:  []string{"private"},
		AllowedEnv:      []string{"PATH"},
	}}
	guard, err = NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.Profiles["exec"] = ToolProfile{AllowedCommands: []string{"rm"}}
	returned := guard.Policy()
	returned.Profiles["exec"] = ToolProfile{AllowedCommands: []string{"curl"}}
	if got := guard.Policy().Profiles["exec"].AllowedCommands[0]; got != "go" {
		t.Fatalf("guard policy alias leaked through defensive copy: %q", got)
	}
	profile := guard.ToolProfile("namespace/tools__exec")
	profile.AllowedDomains[0] = "mutated.example"
	profile.DeniedCommands[0] = "mutated"
	profile.AllowedCommands[0] = "mutated"
	profile.ForbiddenPaths[0] = "mutated"
	profile.AllowedEnv[0] = "MUTATED"
	if got := guard.ToolProfile("exec"); got.AllowedDomains[0] != "example.com" ||
		got.DeniedCommands[0] != "rm" || got.AllowedCommands[0] != "go" ||
		got.ForbiddenPaths[0] != "private" || got.AllowedEnv[0] != "PATH" {
		t.Fatalf("guard tool profile alias leaked through defensive copy: %+v", got)
	}
	if err := guard.ReloadPolicy(Policy{}); err == nil {
		t.Fatal("invalid programmatic reload succeeded")
	}
	if got := guard.Policy().Profiles["exec"].AllowedCommands[0]; got != "go" {
		t.Fatalf("failed reload changed active policy: %q", got)
	}

	var nilGuard *Guard
	if got := nilGuard.Policy(); !reflect.DeepEqual(got, Policy{}) {
		t.Fatalf("nil guard policy = %+v", got)
	}
	if got := nilGuard.ToolProfile("exec"); !reflect.DeepEqual(got, ToolProfile{}) {
		t.Fatalf("nil guard tool profile = %+v", got)
	}
	if err := nilGuard.ReloadPolicy(DefaultPolicy()); err == nil {
		t.Fatal("nil guard reload succeeded")
	}
	if _, err := nilGuard.Scan(context.Background(), ScanRequest{}); err == nil {
		t.Fatal("nil guard scan succeeded")
	}
	decision, err := nilGuard.CheckToolPermission(context.Background(), &tool.PermissionRequest{})
	if err != nil || decision.Action != tool.PermissionActionDeny {
		t.Fatalf("nil guard permission must fail closed: %+v err=%v", decision, err)
	}
	decision, err = guard.CheckToolPermission(context.Background(), nil)
	if err != nil || decision.Action != tool.PermissionActionDeny {
		t.Fatalf("nil permission request must fail closed: %+v err=%v", decision, err)
	}

	uninitialized := &Guard{}
	if got := uninitialized.Policy(); !reflect.DeepEqual(got, Policy{}) {
		t.Fatalf("uninitialized guard policy = %+v", got)
	}
	if _, err := uninitialized.Scan(context.Background(), ScanRequest{}); err == nil {
		t.Fatal("uninitialized guard scan succeeded")
	}
}

func TestGuardComposedPolicyFailureAndPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		previous tool.PermissionPolicy
		wantErr  bool
		want     tool.PermissionAction
	}{
		{
			name: "previous failure",
			previous: tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
				return tool.PermissionDecision{}, errors.New("owner policy unavailable")
			}),
			wantErr: true,
			want:    tool.PermissionActionDeny,
		},
		{
			name: "invalid previous decision",
			previous: tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
				return tool.PermissionDecision{Action: "execute"}, nil
			}),
			wantErr: true,
			want:    tool.PermissionActionDeny,
		},
		{
			name: "weaker previous decision",
			previous: tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
				return tool.AllowPermission(), nil
			}),
			want: tool.PermissionActionDeny,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			guard, err := NewDefaultGuard(WithPermissionPolicy(tc.previous))
			if err != nil {
				t.Fatal(err)
			}
			decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				Arguments: []byte(`{"command":"rm -rf /"}`),
			})
			if (err != nil) != tc.wantErr || decision.Action != tc.want {
				t.Fatalf("decision=%+v err=%v", decision, err)
			}
		})
	}

	var captured AuditEvent
	sink := auditSinkFunc(func(_ context.Context, event AuditEvent) error {
		captured = event
		return nil
	})
	previous := tool.PermissionPolicyFunc(func(context.Context, *tool.PermissionRequest) (tool.PermissionDecision, error) {
		return tool.AskPermission("password=hunter2 needs owner approval"), nil
	})
	guard, err := NewDefaultGuard(WithPermissionPolicy(previous), WithAuditSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName: "exec", Arguments: []byte(`{"command":"echo ok"}`),
	})
	if err != nil || decision.Action != tool.PermissionActionAsk || strings.Contains(decision.Reason, "hunter2") {
		t.Fatalf("composed reason was not safely normalized: %+v err=%v", decision, err)
	}
	if !reflect.DeepEqual(captured.RuleIDs, []string{"composed_permission_policy"}) || strings.Contains(captured.Reason, "hunter2") {
		t.Fatalf("unsafe or incomplete audit event: %+v", captured)
	}
}

func TestGuardOutputSanitizerLifecycle(t *testing.T) {
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.AfterToolCallbackStructured(context.Background(), nil); err == nil {
		t.Fatal("nil after-tool arguments were accepted")
	}
	args := &tool.AfterToolArgs{Result: map[string]any{"message": "safe"}}
	callbackResult, err := guard.AfterToolCallbackStructured(context.Background(), args)
	if err != nil || callbackResult.CustomResult != nil {
		t.Fatalf("safe result should not be replaced: %+v err=%v", callbackResult, err)
	}
	got, err := guard.SanitizeToolResult(context.Background(), args)
	if err != nil || !reflect.DeepEqual(got, args.Result) {
		t.Fatalf("safe result should preserve identity: %#v err=%v", got, err)
	}

	guard, err = NewDefaultGuard(WithAuditSink(errorSink{}))
	if err != nil {
		t.Fatal(err)
	}
	secretArgs := &tool.AfterToolArgs{Result: map[string]any{"password": "hunter2"}}
	if result, err := guard.AfterToolCallbackStructured(context.Background(), secretArgs); err == nil || result != nil {
		t.Fatalf("redaction audit failure must fail closed: result=%#v err=%v", result, err)
	}
	if result, err := guard.SanitizeToolResult(context.Background(), secretArgs); err == nil || result != nil {
		t.Fatalf("final sanitizer must fail closed: result=%#v err=%v", result, err)
	}
}

func TestRedactValueEverySupportedContainer(t *testing.T) {
	type nested struct {
		Password string            `json:"password"`
		Notes    []string          `json:"notes"`
		Headers  map[string]string `json:"headers"`
	}
	input := nested{
		Password: "hunter2",
		Notes:    []string{"safe", "api_key=abcd1234"},
		Headers:  map[string]string{"Authorization": "Bearer abcdefghijklmnop", "X-Trace": "public"},
	}
	value, changed := RedactValue(input)
	if !changed {
		t.Fatal("struct secrets were not redacted")
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("hunter2")) || bytes.Contains(data, []byte("abcd1234")) || bytes.Contains(data, []byte("abcdefghijklmnop")) {
		t.Fatalf("secret remains in struct output: %s", data)
	}
	if !bytes.Contains(data, []byte("public")) {
		t.Fatalf("non-secret value was lost: %s", data)
	}

	tests := []struct {
		name        string
		value       any
		wantChanged bool
	}{
		{"nil", nil, false},
		{"plain string", "hello", false},
		{"secret bytes", []byte("password=hunter2"), true},
		{"string map", map[string]string{"client_secret": "hunter2", "safe": "ok"}, true},
		{"interface slice", []any{"ok", map[string]any{"access_token": "secret"}}, true},
		{"typed slice", []string{"api_key=abcd1234"}, true},
		{"array", [2]string{"ok", "password=hunter2"}, true},
		{"non-string-key map", map[int]string{1: "password=hunter2"}, true},
		{"primitive", 42, false},
		{"pointer", &input, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := RedactValue(tc.value)
			if changed != tc.wantChanged {
				t.Fatalf("changed=%v want=%v for %#v", changed, tc.wantChanged, got)
			}
			if tc.name == "primitive" && got != 42 {
				t.Fatalf("primitive changed: %#v", got)
			}
			if tc.wantChanged {
				data, ok := got.([]byte)
				if !ok {
					var err error
					data, err = json.Marshal(got)
					if err != nil {
						t.Fatal(err)
					}
				}
				if bytes.Contains(data, []byte("hunter2")) ||
					bytes.Contains(data, []byte("abcd1234")) ||
					bytes.Contains(data, []byte(`"secret"`)) ||
					!bytes.Contains(data, []byte(redacted)) {
					t.Fatalf("container result was not recursively redacted: %s", data)
				}
			}
		})
	}

	if got, changed := redactValue("visible", "private_key"); !changed || got != redacted {
		t.Fatalf("secret-labeled value = %#v changed=%v", got, changed)
	}
	unsupported := struct{ Stream chan int }{Stream: make(chan int)}
	if got, changed := RedactValue(unsupported); !changed || got != redacted {
		t.Fatalf("non-JSON struct must fail closed: %#v changed=%v", got, changed)
	}
}

func TestRedactValueNamedTextTypesAndSecretMapKeys(t *testing.T) {
	type namedString string
	type namedBytes []byte

	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "named string", value: namedString("password=supersecret")},
		{name: "named bytes", value: namedBytes("password=supersecret")},
		{name: "secret interface map key", value: map[string]any{"password=supersecret": "visible"}},
		{name: "secret string map key", value: map[string]string{"password=supersecret": "visible"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := RedactValue(tc.value)
			if !changed {
				t.Fatal("secret-bearing value was reported unchanged")
			}
			var data []byte
			rv := reflect.ValueOf(got)
			if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
				data = make([]byte, rv.Len())
				for i := range data {
					data[i] = byte(rv.Index(i).Uint())
				}
			} else {
				var err error
				data, err = json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
			}
			if bytes.Contains(data, []byte("supersecret")) ||
				!bytes.Contains(data, []byte(redacted)) {
				t.Fatalf("secret was not redacted: %s", data)
			}
		})
	}
}

func TestRedactValuePreservesStructuredBinaryFields(t *testing.T) {
	type payload struct {
		Label  string `json:"label"`
		Data   []byte `json:"data"`
		Nested *struct {
			Password string `json:"password"`
		} `json:"nested"`
	}
	original := payload{
		Label: "safe",
		Data:  []byte("password=supersecret"),
		Nested: &struct {
			Password string `json:"password"`
		}{Password: "supersecret"},
	}
	redactedValue, changed := RedactValue(original)
	if !changed {
		t.Fatal("structured binary value was not redacted")
	}
	safe, ok := redactedValue.(payload)
	if !ok {
		t.Fatalf("redacted type = %T, want payload", redactedValue)
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(safe.Data), "supersecret") ||
		strings.Contains(string(encoded), "supersecret") {
		t.Fatalf("structured secret leaked: %s / %s", safe.Data, encoded)
	}
	if !strings.Contains(string(safe.Data), redacted) || safe.Nested.Password != redacted {
		t.Fatalf("unexpected structured redaction: %+v", safe)
	}
}

func TestRedactValuePreservesAdditionalReflectContainers(t *testing.T) {
	type namedMap map[string][]byte
	tests := []struct {
		name  string
		value any
	}{
		{name: "named map and secret key", value: namedMap{
			"password=supersecret": []byte("visible"),
		}},
		{name: "integer map key", value: map[int]string{
			1: "password=supersecret",
		}},
		{name: "typed slice", value: []string{"safe", "password=supersecret"}},
		{name: "typed array", value: [2]string{"safe", "password=supersecret"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			safe, changed := RedactValue(tc.value)
			if !changed {
				t.Fatal("value was not redacted")
			}
			encoded, err := json.Marshal(safe)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "supersecret") {
				t.Fatalf("secret leaked: %s", encoded)
			}
		})
	}

	var nilMap map[string]string
	var nilSlice []string
	var nilPointer *string
	for _, value := range []any{nilMap, nilSlice, nilPointer} {
		if _, changed := RedactValue(value); changed {
			t.Fatalf("nil value reported a redaction: %#v", value)
		}
	}
}

type opaqueMarshalValue struct{ secret string }

func (v opaqueMarshalValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"value": v.secret})
}

type invalidJSONMarshalValue struct{ fail bool }

func (v invalidJSONMarshalValue) MarshalJSON() ([]byte, error) {
	if v.fail {
		return nil, errors.New("marshal failed")
	}
	return []byte(`{`), nil
}

type panicJSONMarshalValue struct{}

func (panicJSONMarshalValue) MarshalJSON() ([]byte, error) { panic("json panic") }

type panicTextKey struct{}

func (panicTextKey) MarshalText() ([]byte, error) { panic("text panic") }

type secretTextKey struct{ value string }

func (k secretTextKey) MarshalText() ([]byte, error) { return []byte(k.value), nil }

func TestRedactValueFailsClosedForOpaqueSerialization(t *testing.T) {
	for _, value := range []any{
		opaqueMarshalValue{secret: "password=supersecret"},
		invalidJSONMarshalValue{},
		invalidJSONMarshalValue{fail: true},
		panicJSONMarshalValue{},
		map[secretTextKey]string{{value: "token=supersecret"}: "safe"},
		map[panicTextKey]string{{}: "safe"},
	} {
		safe, changed := RedactValue(value)
		if !changed || safe != redacted {
			t.Fatalf("opaque value did not fail closed: %#v -> %#v", value, safe)
		}
		encoded, err := json.Marshal(safe)
		if err != nil || strings.Contains(string(encoded), "supersecret") {
			t.Fatalf("opaque secret leaked: %s, %v", encoded, err)
		}
	}
	safeTextKey := map[secretTextKey]string{{value: "safe-key"}: "safe"}
	if safe, changed := RedactValue(safeTextKey); changed || !reflect.DeepEqual(safe, safeTextKey) {
		t.Fatalf("safe text-marshaled key changed: %#v", safe)
	}
	unsupportedKey := map[struct{ ID int }]string{{ID: 1}: "safe"}
	if safe, changed := RedactValue(unsupportedKey); !changed || safe != redacted {
		t.Fatalf("unsupported map key did not fail closed: %#v", safe)
	}
	if safeRedactionMapKey(reflect.Value{}, 0, nil) {
		t.Fatal("invalid map key was accepted")
	}
}

func TestSetRedactedReflectValueConversions(t *testing.T) {
	var bytesValue []byte
	if !setRedactedReflectValue(reflect.ValueOf(&bytesValue).Elem(), redacted) ||
		string(bytesValue) != redacted {
		t.Fatalf("byte conversion = %q", bytesValue)
	}
	var integer int
	if !setRedactedReflectValue(reflect.ValueOf(&integer).Elem(), int64(7)) || integer != 7 {
		t.Fatalf("integer conversion = %d", integer)
	}
	var pointer *int
	if !setRedactedReflectValue(reflect.ValueOf(&pointer).Elem(), nil) || pointer != nil {
		t.Fatal("nil pointer conversion failed")
	}
	if setRedactedReflectValue(reflect.ValueOf(integer), redacted) ||
		setRedactedReflectValue(reflect.ValueOf(&integer).Elem(), nil) ||
		setRedactedReflectValue(reflect.ValueOf(&integer).Elem(), struct{}{}) ||
		setRedactedReflectValue(reflect.ValueOf(&integer).Elem(), redacted) {
		t.Fatal("invalid conversion was accepted")
	}
}

func TestInlineInterpreterHelperBranches(t *testing.T) {
	for option, want := range map[string]bool{
		"/c": true, "-lc": true, "-c": true, "--command": false, "-x": false,
	} {
		if got := shellCommandOption(option); got != want {
			t.Fatalf("shell option %q = %v, want %v", option, got, want)
		}
	}
	for _, tc := range []struct {
		req  ScanRequest
		want string
	}{
		{req: ScanRequest{Backend: "container"}, want: "container"},
		{req: ScanRequest{ToolName: "workspace_exec"}, want: "workspaceexec"},
		{req: ScanRequest{ToolName: "host_exec"}, want: "hostexec"},
		{req: ScanRequest{ToolName: "code_execution"}, want: "codeexec"},
		{req: ScanRequest{ToolName: "other"}, want: "unspecified"},
	} {
		if got := effectiveBackend(tc.req); got != tc.want {
			t.Fatalf("backend(%+v) = %q, want %q", tc.req, got, tc.want)
		}
	}
	for name, family := range map[string]string{
		"python2.7": "python", "pypy3": "python", "node18": "node",
		"perl5.38": "perl", "ruby3.2": "ruby", "pwsh": "powershell",
		"bash": "shell", "echo": "",
	} {
		if got := inlineInterpreterFamily(name); got != family {
			t.Fatalf("family(%q) = %q, want %q", name, got, family)
		}
	}
	for _, tc := range []struct {
		name string
		args []string
		code string
	}{
		{name: "python2.7", args: []string{"-c", "print('ok')"}, code: "print('ok')"},
		{name: "node", args: []string{"--print=process.env"}, code: "process.env"},
		{name: "perl", args: []string{"-eexec('id')"}, code: "exec('id')"},
		{name: "ruby", args: []string{"-e", "ENV['TOKEN']"}, code: "ENV['TOKEN']"},
		{name: "pwsh", args: []string{"-command=Write-Output ok"}, code: "Write-Output ok"},
		{name: "bash", args: []string{"-c", "echo ok"}, code: "echo ok"},
	} {
		code, opaque, ok := inlineInterpreterPayload(tc.name, tc.args)
		if !ok || opaque || code != tc.code {
			t.Fatalf("payload(%q) = %q, %v, %v", tc.name, code, opaque, ok)
		}
	}
	if _, _, ok := inlineInterpreterPayload("python", []string{"-c"}); ok {
		t.Fatal("missing inline payload was accepted")
	}
	if _, opaque, ok := inlineInterpreterPayload("pwsh", []string{"-enc=AAAA"}); !ok || !opaque {
		t.Fatal("encoded PowerShell was not treated as opaque")
	}
	var decision tool.PermissionAction
	scanInlineInterpreterDepth("echo ok", func(_ string, _ Severity, action tool.PermissionAction, _, _ string) {
		decision = action
	}, 5)
	if decision != tool.PermissionActionAsk {
		t.Fatalf("nested depth decision = %q", decision)
	}
}

func TestSanitizeToolErrorBranches(t *testing.T) {
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.SanitizeToolError(context.Background(), nil); err == nil {
		t.Fatal("nil arguments were accepted")
	}
	if safe, err := guard.SanitizeToolError(context.Background(), &tool.AfterToolArgs{}); err != nil || safe != nil {
		t.Fatalf("nil tool error = %v, %v", safe, err)
	}
	plain := errors.New("ordinary failure")
	if safe, err := guard.SanitizeToolError(context.Background(), &tool.AfterToolArgs{Error: plain}); err != nil || safe != plain {
		t.Fatalf("ordinary error changed: %v, %v", safe, err)
	}

	auditCount := 0
	guard, err = NewDefaultGuard(WithAuditSink(auditSinkFunc(func(context.Context, AuditEvent) error {
		auditCount++
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	original := errors.New("password=supersecret")
	safe, err := guard.SanitizeToolError(context.Background(), &tool.AfterToolArgs{
		ToolName: "demo", Arguments: []byte(`{}`), Error: original,
	})
	if err != nil || safe == nil || strings.Contains(safe.Error(), "supersecret") {
		t.Fatalf("sanitized error = %v, %v", safe, err)
	}
	if errors.Unwrap(safe) != nil {
		t.Fatal("sanitized error exposed its original cause through errors.Unwrap")
	}
	if strings.Contains(fmt.Sprintf("%#v", safe), "supersecret") {
		t.Fatal("sanitized error exposed its original cause through Go-syntax formatting")
	}
	if auditCount != 1 {
		t.Fatalf("redaction audit count = %d", auditCount)
	}

	guard, err = NewDefaultGuard(WithAuditSink(errorSink{}))
	if err != nil {
		t.Fatal(err)
	}
	if safe, err := guard.SanitizeToolError(context.Background(), &tool.AfterToolArgs{Error: original}); err == nil || safe != nil {
		t.Fatalf("audit failure did not fail closed: %v, %v", safe, err)
	}
}

func TestRedactStringRecognizesCredentialFamilies(t *testing.T) {
	privateKey := "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----"
	for _, secret := range []string{
		"sk-abcdefghijklmnop",
		"github_pat_abcdefghijklmnop",
		"AKIA" + "ABCDEFGHIJKLMNOP",
		"Bearer abcdefghijklmnop",
		privateKey,
		"client_secret=abcd1234",
		"password=x",
		"token=abc",
		"api_key=1",
	} {
		if got := RedactString(secret); got == secret || !strings.Contains(got, redacted) {
			t.Errorf("credential family was not redacted: %q -> %q", secret, got)
		}
	}
	for _, input := range []string{
		`password="alpha beta"`,
		`password='alpha beta'`,
	} {
		got := RedactString(input)
		if strings.Contains(got, "alpha") || strings.Contains(got, "beta") || !strings.Contains(got, redacted) {
			t.Fatalf("quoted secret was not fully redacted: %q", got)
		}
	}
	value, changed := RedactValue(map[string]any{
		"author": "Ada", "authority": "local", "secretary": "Grace",
	})
	if changed || !reflect.DeepEqual(value, map[string]any{
		"author": "Ada", "authority": "local", "secretary": "Grace",
	}) {
		t.Fatalf("ordinary metadata key was redacted: %#v changed=%v", value, changed)
	}
}

func TestScanFailsClosedForIncompleteNestedInput(t *testing.T) {
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	deep := any("leaf")
	for i := 0; i < maxCollectedInputDepth+2; i++ {
		deep = []any{deep}
	}
	for name, raw := range map[string]any{
		"cycle":       cycle,
		"deep":        deep,
		"unsupported": make(chan int),
	} {
		t.Run(name, func(t *testing.T) {
			report, err := guard.Scan(context.Background(), ScanRequest{
				ToolName: "custom", RawFields: map[string]any{"value": raw},
			})
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tool.PermissionActionDeny || !hasFindingRule(report.Findings, "secret_exposure") {
				t.Fatalf("incomplete input did not fail closed: %+v", report)
			}
		})
	}
}

func TestScanNormalizesResourceAndGitNetworkRisks(t *testing.T) {
	policy := DefaultPolicy()
	policy.Profiles = map[string]ToolProfile{"exec": {AllowedDomains: []string{"api.github.com"}}}
	guard, err := NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"sleep 120s", "sleep 2m", "sleep 1m 60s", "sleep infinity"} {
		report, err := guard.Scan(context.Background(), ScanRequest{ToolName: "exec", Command: command})
		if err != nil || report.Decision != tool.PermissionActionDeny {
			t.Fatalf("%q should be denied: %+v err=%v", command, report, err)
		}
	}
	report, err := guard.Scan(context.Background(), ScanRequest{ToolName: "exec", Command: "sleep 119s"})
	if err != nil || report.Decision != tool.PermissionActionAllow {
		t.Fatalf("short sleep should remain allowed: %+v err=%v", report, err)
	}
	for _, command := range []string{"git ls-remote git@evil.example:repo"} {
		report, err = guard.Scan(context.Background(), ScanRequest{ToolName: "exec", Command: command})
		if err != nil || report.Decision != tool.PermissionActionDeny {
			t.Fatalf("%q should be denied: %+v err=%v", command, report, err)
		}
	}
	for _, command := range []string{"git -C repo fetch origin", "git submodule update --remote"} {
		report, err = guard.Scan(context.Background(), ScanRequest{ToolName: "exec", Command: command})
		if err != nil || report.Decision != tool.PermissionActionAsk {
			t.Fatalf("%q should require confirmation: %+v err=%v", command, report, err)
		}
	}
}

func TestReportMarksMetadataRedaction(t *testing.T) {
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	report, err := guard.Scan(context.Background(), ScanRequest{
		ToolName: "password=x", Backend: "token=abc", Command: "echo safe",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Redacted || strings.Contains(report.ToolName+report.Backend, "=x") || strings.Contains(report.ToolName+report.Backend, "=abc") {
		t.Fatalf("metadata redaction was not reported: %+v", report)
	}
}

func TestJSONLSinkLifecycleAndFailureBranches(t *testing.T) {
	if _, err := NewJSONLSink(""); err == nil {
		t.Fatal("empty audit path was accepted")
	}
	missingParent := filepath.Join(t.TempDir(), "missing", "audit.jsonl")
	if _, err := NewJSONLSink(missingParent); err == nil {
		t.Fatal("sink should not create missing parent directories")
	}

	path := filepath.Join(t.TempDir(), "audit.jsonl")
	sink, err := NewJSONLSink(path)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sink.WriteAudit(cancelled, AuditEvent{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled audit write error = %v", err)
	}
	if err := sink.WriteAudit(context.Background(), AuditEvent{Decision: tool.PermissionActionAllow, RequestID: "digest"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close must be idempotent: %v", err)
	}

	zero := &JSONLSink{}
	if err := zero.Close(); err != nil {
		t.Fatalf("zero-value Close = %v", err)
	}
	if err := zero.WriteAudit(context.Background(), AuditEvent{}); err == nil {
		t.Fatal("zero-value sink accepted a write")
	}

	file, err := os.CreateTemp(t.TempDir(), "closed-audit-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	writeBroken := &JSONLSink{file: file}
	if err := writeBroken.WriteAudit(context.Background(), AuditEvent{}); err == nil || !strings.Contains(err.Error(), "append audit event") {
		t.Fatalf("closed descriptor should surface append failure: %v", err)
	}
	broken := &JSONLSink{file: file}
	if err := broken.Close(); err == nil || !strings.Contains(err.Error(), "sync audit file") {
		t.Fatalf("closed descriptor should surface sync failure: %v", err)
	}
}

func TestGuardEmitsSafeOpenTelemetryAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	ctx, span := provider.Tracer("safety-test").Start(context.Background(), "scan")
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	report, err := guard.Scan(ctx, ScanRequest{
		ToolName: "exec", Backend: "api_key=supersecret", Command: "password=hunter2",
	})
	if err != nil {
		t.Fatal(err)
	}
	span.End()
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d", len(ended))
	}
	attrs := make(map[string]string)
	for _, attr := range ended[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.Emit()
	}
	for _, key := range []string{
		"tool.safety.decision", "tool.safety.risk", "tool.safety.rule",
		"tool.safety.rules", "tool.safety.blocked", "tool.safety.backend",
		"tool.safety.request_sha256", "tool.safety.duration_us",
	} {
		if _, ok := attrs[key]; !ok {
			t.Errorf("missing OTel attribute %q: %#v", key, attrs)
		}
	}
	serialized := fmt.Sprint(attrs)
	if strings.Contains(serialized, "hunter2") || strings.Contains(serialized, "supersecret") || attrs["tool.safety.request_sha256"] != report.RequestID {
		t.Fatalf("unsafe or inconsistent OTel attributes: %s", serialized)
	}
	if attrs["tool.safety.blocked"] != "true" || !strings.Contains(attrs["tool.safety.rules"], "secret_exposure") {
		t.Fatalf("blocked scan attributes are incomplete: %#v", attrs)
	}

	ctx, span = provider.Tracer("safety-test").Start(context.Background(), "safe-scan")
	if _, err := guard.Scan(ctx, ScanRequest{Command: "echo ok"}); err != nil {
		t.Fatal(err)
	}
	span.End()
	ended = recorder.Ended()
	safeAttrs := make(map[string]string)
	for _, attr := range ended[len(ended)-1].Attributes() {
		safeAttrs[string(attr.Key)] = attr.Value.Emit()
	}
	if safeAttrs["tool.safety.risk"] != "none" || safeAttrs["tool.safety.rule"] != "" || safeAttrs["tool.safety.blocked"] != "false" {
		t.Fatalf("safe scan attributes = %#v", safeAttrs)
	}
}

func TestAuditFailureOpenTelemetryIsDeniedAndBlocked(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	ctx, span := provider.Tracer("safety-test").Start(context.Background(), "audit-failure")
	guard, err := NewDefaultGuard(WithAuditSink(errorSink{}))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := guard.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName: "exec", Arguments: []byte(`{"command":"echo ok"}`),
	})
	if err == nil || decision.Action != tool.PermissionActionDeny {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	span.End()
	attrs := make(map[string]string)
	for _, attr := range recorder.Ended()[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.Emit()
	}
	if attrs["tool.safety.decision"] != "deny" || attrs["tool.safety.blocked"] != "true" {
		t.Fatalf("audit failure attributes = %#v", attrs)
	}
}

func TestScanPolicyOverridesAndExecutionModes(t *testing.T) {
	disabled := false
	policy := DefaultPolicy()
	policy.Rules.DangerousCommand.Enabled = &disabled
	policy.Rules.ShellBypass.Enabled = &disabled
	guard, err := NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	report, err := guard.Scan(context.Background(), ScanRequest{Command: "rm -rf /"})
	if err != nil || report.Decision != tool.PermissionActionAllow || len(report.Findings) != 0 {
		t.Fatalf("disabled rules should not emit findings: %+v err=%v", report, err)
	}

	policy = DefaultPolicy()
	policy.DefaultAction = tool.PermissionActionAsk
	policy.Rules.DangerousCommand.Action = tool.PermissionActionAllow
	policy.Rules.ShellBypass.Enabled = &disabled
	guard, err = NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	report, err = guard.Scan(context.Background(), ScanRequest{Command: "rm -rf /"})
	if err != nil || report.Decision != tool.PermissionActionAsk || !report.Blocked || report.Reason == "" {
		t.Fatalf("default ask should remain stronger than allow finding: %+v err=%v", report, err)
	}
	if len(report.Findings) != 2 || report.Findings[0].Action != tool.PermissionActionAsk || report.Recommendation == "No action required." {
		t.Fatalf("custom rule action was not preserved: %+v", report)
	}

	policy = DefaultPolicy()
	policy.Profiles = map[string]ToolProfile{"exec": {AllowPTY: true, AllowBackground: true, AllowHost: true}}
	guard, err = NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	report, err = guard.Scan(context.Background(), ScanRequest{
		ToolName: "exec", Backend: "hostexec", PTY: true, Background: true, Command: "echo ok",
	})
	if err != nil || report.Decision != tool.PermissionActionAllow {
		t.Fatalf("explicitly allowed execution modes were blocked: %+v err=%v", report, err)
	}
	report, err = guard.Scan(context.Background(), ScanRequest{Timeout: -time.Second})
	if err != nil || report.Decision != tool.PermissionActionDeny || !hasRule(report, "resource_abuse") {
		t.Fatalf("negative timeout was not denied: %+v err=%v", report, err)
	}
}

func TestScanCodeCommandAndProfileBranches(t *testing.T) {
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		code string
		rule string
		want tool.PermissionAction
	}{
		{"blank", " \n", "", tool.PermissionActionAllow},
		{"dynamic execution", `subprocess.run(["echo","ok"])`, "shell_bypass", tool.PermissionActionAsk},
		{"network code", `requests.get("https://example.com")`, "network_access", tool.PermissionActionAsk},
		{"destructive code", `os.unlink("data.txt")`, "dangerous_command", tool.PermissionActionDeny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := guard.Scan(context.Background(), ScanRequest{Code: tc.code})
			if err != nil || report.Decision != tc.want {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			if tc.rule != "" && !hasRule(report, tc.rule) {
				t.Fatalf("missing %s: %+v", tc.rule, report)
			}
		})
	}

	policy := DefaultPolicy()
	policy.Profiles = map[string]ToolProfile{"exec": {
		AllowedCommands: []string{"go"}, DeniedCommands: []string{"rm"}, AllowedEnv: []string{"PATH"},
	}}
	guard, err = NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []ScanRequest{
		{ToolName: "exec", Command: "rm file"},
		{ToolName: "exec", Command: "'unterminated"},
	} {
		report, err := guard.Scan(context.Background(), req)
		if err != nil || report.Decision == tool.PermissionActionAllow || !hasRule(report, "shell_bypass") {
			t.Fatalf("configured shell policy escaped: %+v err=%v", report, err)
		}
	}
	report, err := guard.Scan(context.Background(), ScanRequest{ToolName: "exec", Command: "go test ./...", Env: map[string]string{"CI": "1"}})
	if err != nil || report.Decision != tool.PermissionActionDeny {
		t.Fatalf("environment outside the allowlist was not blocked: %+v err=%v", report, err)
	}
	for _, name := range []string{"PATH", "path", "LD_PRELOAD", "BASH_ENV"} {
		report, err = guard.Scan(context.Background(), ScanRequest{
			ToolName: "exec", Command: "go test ./...", Env: map[string]string{name: "controlled"},
		})
		if err != nil || report.Decision != tool.PermissionActionDeny || !hasRule(report, "host_execution") {
			t.Fatalf("dangerous environment %q was not blocked: %+v err=%v", name, report, err)
		}
	}
}

func TestRedactValueTerminatesOnCycles(t *testing.T) {
	cyclicMap := map[string]any{"password": "hunter2"}
	cyclicMap["self"] = cyclicMap
	got, changed := RedactValue(cyclicMap)
	if !changed {
		t.Fatal("cyclic value was not safely replaced")
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("redacted cyclic map is not JSON serializable: %v", err)
	}
	if bytes.Contains(data, []byte("hunter2")) || !bytes.Contains(data, []byte(redacted)) {
		t.Fatalf("cyclic map redaction leaked data: %s", data)
	}

	cyclicSlice := make([]any, 1)
	cyclicSlice[0] = cyclicSlice
	if got, changed = RedactValue(cyclicSlice); !changed {
		t.Fatal("cyclic slice was not safely replaced")
	} else if _, err := json.Marshal(got); err != nil {
		t.Fatalf("redacted cyclic slice is not JSON serializable: %v", err)
	}
}

func TestNetworkAndDestinationHelperBoundaries(t *testing.T) {
	policy := DefaultPolicy()
	policy.Profiles = map[string]ToolProfile{"net": {AllowedDomains: []string{"example.com"}}}
	guard, err := NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, command string
		want          tool.PermissionAction
	}{
		{"missing destination", "curl", tool.PermissionActionAsk},
		{"duplicate destination", "curl https://example.com https://example.com", tool.PermissionActionAllow},
		{"trailing URL punctuation", "curl https://example.com/path).", tool.PermissionActionAsk},
		{"bracketed IPv6 not allowlisted", "ssh user@[2001:db8::1]", tool.PermissionActionDeny},
		{"config filename ignored as host", "curl --config request.conf https://example.com", tool.PermissionActionAsk},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := guard.Scan(context.Background(), ScanRequest{ToolName: "net", Command: tc.command})
			if err != nil || report.Decision != tc.want {
				t.Fatalf("decision=%s want=%s report=%+v err=%v", report.Decision, tc.want, report, err)
			}
		})
	}

	for _, tc := range []struct {
		raw, want string
		wantErr   bool
	}{
		{"HTTPS://Example.COM./path).", "example.com", false},
		{"https://192.0.2.1/path", "192.0.2.1", false},
		{"https://", "", true},
		{"user@example.com:22/repo", "example.com", false},
		{"[2001:db8::1]:22", "2001:db8::1", false},
		{"user@", "", true},
		{"host.example.", "host.example", false},
	} {
		host, err := destinationHost(tc.raw)
		if host != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("destinationHost(%q)=(%q,%v), want (%q, err=%v)", tc.raw, host, err, tc.want, tc.wantErr)
		}
	}
}

func TestScanCollectionAndSmallHelperContracts(t *testing.T) {
	if writeStdinHasInput(ScanRequest{}) {
		t.Fatal("empty write request must remain a non-mutating poll")
	}
	var texts []scanText
	collectAny("nil", nil, &texts)
	collectAny("bytes", []byte("hello"), &texts)
	collectAny("list", []any{"one", 2}, &texts)
	collectAny("map", map[string]any{"b": "two", "a": `{"nested":"three"}`}, &texts)
	collectAny("number", 3, &texts)
	collectAny("bad", make(chan int), &texts)
	combined := combineTexts(append(texts, scanText{label: "path", value: `C:\\tmp\\.\\file`}))
	for _, want := range []string{"hello", "one", "three", "3", "C:/tmp/file"} {
		if !strings.Contains(combined, want) {
			t.Errorf("combined scan text missing %q: %q", want, combined)
		}
	}

	if got := profileFor(Policy{Profiles: map[string]ToolProfile{"run": {MaxOutputBytes: 1}}}, "server/tools__run"); got.MaxOutputBytes != 1 {
		t.Fatalf("qualified tool profile was not resolved: %+v", got)
	}
	if got := profileFor(Policy{Profiles: map[string]ToolProfile{"*": {MaxOutputBytes: 2}}}, "unknown"); got.MaxOutputBytes != 2 {
		t.Fatalf("wildcard profile was not resolved: %+v", got)
	}
	if got := policyRule(BuiltinRules{}, "unknown"); !reflect.DeepEqual(got, RulePolicy{}) {
		t.Fatalf("unknown rule = %+v", got)
	}
	if actionRank("") != 0 || severityRank("") != 0 || severityRank(SeverityLow) != 1 || severityRank(SeverityMedium) != 2 {
		t.Fatal("ranking helpers must preserve documented ordering")
	}
	if !stronger(tool.PermissionActionDeny, tool.PermissionActionAsk) || stronger(tool.PermissionActionAllow, tool.PermissionActionAsk) {
		t.Fatal("permission precedence must remain deny > ask > allow")
	}
	if first(nil) != "" || firstNonEmpty("", "second") != "second" || firstNonEmpty("", "") != "" {
		t.Fatal("evidence selection helpers returned an unsafe value")
	}
	if recommendationFor(Finding{Action: tool.PermissionActionAsk}) == recommendationFor(Finding{Action: tool.PermissionActionDeny}) {
		t.Fatal("ask and deny recommendations must be distinct")
	}
}

func TestDestructiveRMAndSSHForwardParsingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
		want    bool
	}{
		{"", []string{"rm.exe", "--recursive", "--force", "C:/Windows"}, true},
		{"rm", []string{"-R", "/etc"}, true},
		{"rm", []string{"-f", "local.txt"}, false},
		{"'unterminated", nil, false},
		{"echo rm -rf /", nil, false},
		{"rm -rf ../..", nil, true},
		{"rm -rf ../../etc", nil, true},
		{"rm -rf foo/../..", nil, true},
		{"rm -rf ./x/../../..", nil, true},
		{"rm -rf C:/foo/../..", nil, true},
		{"rm -rf //?/C:/Windows", nil, true},
		{"rm -rf ~", nil, true},
		{"rm -rf /*", nil, true},
		{"rm -rf /etc*", nil, true},
		{"rm -rf C:/*", nil, true},
		{"rm -rf ./build", nil, false},
	} {
		if got := destructiveRM(tc.command, tc.args); got != tc.want {
			t.Errorf("destructiveRM(%q,%#v)=%v want=%v", tc.command, tc.args, got, tc.want)
		}
	}
	if !destructiveRM("rm -rf ../..", nil, "/home/student/project") {
		t.Fatal("parent traversal with an explicit working directory was not protected")
	}
	if !destructiveRM("rm -rf safe/../..", nil, ".") {
		t.Fatal("normalized parent traversal with a relative working directory was not protected")
	}

	got := sshForwardDestinations("ssh -W=api.example:443 -L=8080:web.example:80 -R 9000:db.example:5432 -D 1080 host")
	for _, want := range []string{"api.example", "web.example", "db.example"} {
		if !containsString(got, want) {
			t.Errorf("forward destinations %#v missing %q", got, want)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type auditSinkFunc func(context.Context, AuditEvent) error

func (f auditSinkFunc) WriteAudit(ctx context.Context, event AuditEvent) error {
	return f(ctx, event)
}
