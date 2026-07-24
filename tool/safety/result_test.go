// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	resultSecret = "sk-result-secret-value"
	errorSecret  = "ghp_abcdefghijklmnopqrstuvwxyz123456"
)

func TestResultProcessorRedactsStructuredValuesWithoutMutation(t *testing.T) {
	type credentials struct {
		Password string `json:"password"`
		Safe     string `json:"safe"`
	}
	input := map[string]any{
		"struct": credentials{Password: resultSecret, Safe: "visible"},
		"typed": map[string]string{
			"API_KEY": resultSecret,
			"note":    "bearer " + errorSecret,
		},
		"nested": []any{
			map[string]any{"Credentials": resultSecret},
			[2]string{"safe", "token=" + resultSecret},
		},
		"number": json.Number("9007199254740993"),
	}
	processor := mustResultProcessor(t, 4096, nil)
	preflight := validResultPreflight()

	processed, err := processor.Process(context.Background(), preflight, input, nil)

	require.NoError(t, err)
	require.True(t, processed.Redacted)
	require.False(t, processed.Truncated)
	encoded, err := json.Marshal(processed)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), resultSecret)
	require.NotContains(t, string(encoded), errorSecret)
	require.Contains(t, string(encoded), "visible")
	tree := processed.Value.(map[string]any)
	require.Equal(t, json.Number("9007199254740993"), tree["number"])

	// Processing is performed on a JSON copy, not the caller-owned input.
	require.Equal(t, resultSecret, input["struct"].(credentials).Password)
	require.Equal(t, resultSecret, input["typed"].(map[string]string)["API_KEY"])
}

func TestResultProcessorRedactsCaseTagVariantsAndEmbeddedSecrets(t *testing.T) {
	type tagged struct {
		Private string `json:"privateKey"`
		Token   string `json:"Access-Token"`
	}
	input := map[string]any{
		"tagged": tagged{Private: resultSecret, Token: errorSecret},
		"text":   "authorization: Basic dXNlcjpwYXNzd29yZA==",
		"path":   "/home/user/.ssh/id_rsa",
	}
	processor := mustResultProcessor(t, 4096, nil)

	processed, err := processor.Process(
		context.Background(), validResultPreflight(), input, nil,
	)

	require.NoError(t, err)
	require.True(t, processed.Redacted)
	encoded, err := json.Marshal(processed)
	require.NoError(t, err)
	for _, secret := range []string{
		resultSecret, errorSecret, "dXNlcjpwYXNzd29yZA==", ".ssh/id_rsa",
	} {
		require.NotContains(t, string(encoded), secret)
	}
}

func TestResultProcessorRedactsNormalizedSensitiveFieldNames(t *testing.T) {
	type tagged struct {
		PrivateSpace string `json:"private key"`
		PrivateDot   string `json:"private.key"`
		APIKeySpace  string `json:"api key"`
		APIKeyDot    string `json:"api.key"`
		AccessDash   string `json:"access-key"`
		APIKeySnake  string `json:"API_KEY"`
		MixedCase    string `json:"clientSecret"`
	}
	input := map[string]any{
		"struct": tagged{
			PrivateSpace: "raw-private-space",
			PrivateDot:   "raw-private-dot",
			APIKeySpace:  "raw-api-space",
			APIKeyDot:    "raw-api-dot",
			AccessDash:   "raw-access-dash",
			APIKeySnake:  "raw-api-snake",
			MixedCase:    "raw-client-secret",
		},
		"map": map[string]string{
			"Private_Key": "raw-map-private",
			"access key":  "raw-map-access",
			"api.key":     "raw-map-api",
		},
	}
	processor := mustResultProcessor(t, 4096, nil)

	processed, err := processor.Process(
		context.Background(), validResultPreflight(), input, nil,
	)

	require.NoError(t, err)
	require.True(t, processed.Redacted)
	encoded, marshalErr := json.Marshal(processed)
	require.NoError(t, marshalErr)
	for _, raw := range []string{
		"raw-private-space", "raw-private-dot", "raw-api-space",
		"raw-api-dot", "raw-access-dash", "raw-api-snake",
		"raw-client-secret", "raw-map-private", "raw-map-access",
		"raw-map-api",
	} {
		require.NotContains(t, string(encoded), raw)
	}
}

func TestResultProcessorDoesNotOverclassifySensitiveSubstrings(t *testing.T) {
	input := map[string]string{
		"tokenizer":         "parser",
		"monkeytokenbucket": "safe-value",
		"secretary":         "visible",
		"passwordless":      "enabled",
		"publicKey":         "public-material",
		"authtokenizer":     "auth-parser",
		"oauthsecretary":    "oauth-contact",
		"dbpasswordless":    "database-mode",
		"mypublickey":       "public-key-material",
		"credentialed":      "feature-enabled",
		"credentialing":     "workflow-name",
		"uncredentialed":    "state-disabled",
		"noncredentialed":   "state-not-configured",
	}
	processor := mustResultProcessor(t, 4096, nil)

	processed, err := processor.Process(
		context.Background(), validResultPreflight(), input, nil,
	)

	require.NoError(t, err)
	require.False(t, processed.Redacted)
	require.Equal(t, map[string]any{
		"tokenizer":         "parser",
		"monkeytokenbucket": "safe-value",
		"secretary":         "visible",
		"passwordless":      "enabled",
		"publicKey":         "public-material",
		"authtokenizer":     "auth-parser",
		"oauthsecretary":    "oauth-contact",
		"dbpasswordless":    "database-mode",
		"mypublickey":       "public-key-material",
		"credentialed":      "feature-enabled",
		"credentialing":     "workflow-name",
		"uncredentialed":    "state-disabled",
		"noncredentialed":   "state-not-configured",
	}, processed.Value)
}

func TestResultProcessorPreservesCompactSensitiveNameRecognition(t *testing.T) {
	input := map[string]string{
		"authtoken":         "opaque-compact-1",
		"oauthtoken":        "opaque-compact-2",
		"oauthsecret":       "opaque-compact-3",
		"dbpassword":        "opaque-compact-4",
		"userpasswd":        "opaque-compact-5",
		"myapikey":          "opaque-compact-6",
		"myprivatekey":      "opaque-compact-7",
		"githubtoken":       "opaque-compact-8",
		"serviceaccesskey":  "opaque-compact-9",
		"signingprivatekey": "opaque-compact-10",
	}
	processor := mustResultProcessor(t, 4096, nil)

	processed, err := processor.Process(
		context.Background(), validResultPreflight(), input, nil,
	)

	require.NoError(t, err)
	require.True(t, processed.Redacted)
	encoded, marshalErr := json.Marshal(processed)
	require.NoError(t, marshalErr)
	for index := 1; index <= len(input); index++ {
		require.NotContains(t, string(encoded), fmt.Sprintf("opaque-compact-%d", index))
	}
}

func TestResultProcessorRedactsCompactCredentialNames(t *testing.T) {
	input := map[string]string{
		"awscredentials":          "opaque-credential-01",
		"clientcredentialsdata":   "opaque-credential-02",
		"credentialsfile":         "opaque-credential-03",
		"awscredential":           "opaque-credential-04",
		"clientcredential":        "opaque-credential-05",
		"googlecredentialsconfig": "opaque-credential-06",
		"credentialfile":          "opaque-credential-07",
		"credentialdata":          "opaque-credential-08",
		"awscredentialfile":       "opaque-credential-09",
		"oauthcredentialconfig":   "opaque-credential-10",
	}
	processor := mustResultProcessor(t, 4096, nil)

	processed, err := processor.Process(
		context.Background(), validResultPreflight(), input, nil,
	)

	require.NoError(t, err)
	require.True(t, processed.Redacted)
	encoded, marshalErr := json.Marshal(processed)
	require.NoError(t, marshalErr)
	for index := 1; index <= len(input); index++ {
		require.NotContains(t, string(encoded), fmt.Sprintf("opaque-credential-%02d", index))
	}
}

func TestResultProcessorRedactsSensitiveWordTokensWithSuffixes(t *testing.T) {
	type suffixed struct {
		PasswordHash  string `json:"passwordHash"`
		PasswordSpace string `json:"password hash"`
		TokenSnake    string `json:"token_value"`
		TokenSpace    string `json:"token value"`
		APIKeyValue   string `json:"api key value"`
		PrivateKeyPEM string `json:"privateKeyPEM"`
		MixedCase     string `json:"Client.SECRET-data"`
	}
	input := map[string]any{
		"struct": suffixed{
			PasswordHash:  "raw-password-hash",
			PasswordSpace: "raw-password-space",
			TokenSnake:    "raw-token-snake",
			TokenSpace:    "raw-token-space",
			APIKeyValue:   "raw-api-key-value",
			PrivateKeyPEM: "raw-private-key-pem",
			MixedCase:     "raw-client-secret-data",
		},
		"map": map[string]string{
			"ACCESS-key-data":    "raw-access-key-data",
			"credentials_file":   "raw-credentials-file",
			"client secret hash": "raw-client-secret-hash",
		},
	}
	processor := mustResultProcessor(t, 4096, nil)

	processed, err := processor.Process(
		context.Background(), validResultPreflight(), input, nil,
	)

	require.NoError(t, err)
	require.True(t, processed.Redacted)
	encoded, marshalErr := json.Marshal(processed)
	require.NoError(t, marshalErr)
	for _, raw := range []string{
		"raw-password-hash", "raw-password-space", "raw-token-snake",
		"raw-token-space", "raw-api-key-value", "raw-private-key-pem",
		"raw-client-secret-data", "raw-access-key-data",
		"raw-credentials-file", "raw-client-secret-hash",
	} {
		require.NotContains(t, string(encoded), raw)
	}
}

func TestResultProcessorRedactsCodeExecutorFiles(t *testing.T) {
	input := codeexecutor.CodeExecutionResult{
		Output: "token=" + resultSecret,
		OutputFiles: []codeexecutor.File{{
			Name:     "/tmp/.env",
			Content:  "-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----",
			MIMEType: "text/plain",
		}},
	}
	processor := mustResultProcessor(t, 4096, nil)

	processed, err := processor.Process(
		context.Background(), validResultPreflight(), input, nil,
	)

	require.NoError(t, err)
	require.True(t, processed.Redacted)
	encoded, err := json.Marshal(processed)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), resultSecret)
	require.NotContains(t, string(encoded), ".env")
	require.NotContains(t, string(encoded), "BEGIN PRIVATE KEY")
	require.Contains(t, string(encoded), "text/plain")
}

func TestResultProcessorExecutionErrorIsSingleLineAndRedacted(t *testing.T) {
	sink := &recordingAuditSink{}
	processor := mustResultProcessor(t, 4096, sink)
	rawError := "request failed\npassword=" + resultSecret + "\r\ntoken=" + errorSecret

	processed, err := processor.Process(
		context.Background(), validResultPreflight(), "safe", errors.New(rawError),
	)

	require.NoError(t, err)
	require.True(t, processed.Redacted)
	require.NotContains(t, processed.ExecutionError, "\n")
	require.NotContains(t, processed.ExecutionError, "\r")
	require.NotContains(t, processed.ExecutionError, resultSecret)
	require.NotContains(t, processed.ExecutionError, errorSecret)
	events := sink.snapshot()
	require.Len(t, events, 1)
	eventJSON, marshalErr := json.Marshal(events[0])
	require.NoError(t, marshalErr)
	require.NotContains(t, string(eventJSON), rawError)
	require.NotContains(t, string(eventJSON), resultSecret)
	require.NotContains(t, string(eventJSON), errorSecret)
}

func TestResultProcessorHandlesNilResultAndError(t *testing.T) {
	processor := mustResultProcessor(t, 4096, nil)

	processed, err := processor.Process(
		context.Background(), validResultPreflight(), nil, nil,
	)

	require.NoError(t, err)
	require.Nil(t, processed.Value)
	require.Empty(t, processed.ExecutionError)
	require.False(t, processed.Redacted)
	require.False(t, processed.Truncated)
}

func TestResultProcessorEnforcesCompleteSerializedBudget(t *testing.T) {
	for _, input := range []any{
		map[string]any{strings.Repeat("k", 1024): "small"},
		[]json.Number{
			json.Number("123456789012345678901234567890"),
			json.Number("123456789012345678901234567890"),
			json.Number("123456789012345678901234567890"),
			json.Number("123456789012345678901234567890"),
			json.Number("123456789012345678901234567890"),
			json.Number("123456789012345678901234567890"),
			json.Number("123456789012345678901234567890"),
			json.Number("123456789012345678901234567890"),
		},
	} {
		const limit = 180
		processor := mustResultProcessor(t, limit, nil)

		processed, err := processor.Process(
			context.Background(), validResultPreflight(), input, nil,
		)

		require.NoError(t, err)
		require.True(t, processed.Truncated)
		require.Equal(t, safeOmittedResultValue(), processed.Value)
		encoded, marshalErr := json.Marshal(processed)
		require.NoError(t, marshalErr)
		require.LessOrEqual(t, len(encoded), limit)
	}
}

func TestResultProcessorBudgetIncludesExecutionError(t *testing.T) {
	const limit = 180
	processor := mustResultProcessor(t, limit, nil)

	processed, err := processor.Process(
		context.Background(), validResultPreflight(), "safe",
		errors.New(strings.Repeat("execution failed ", 200)),
	)

	require.NoError(t, err)
	require.True(t, processed.Truncated)
	require.Empty(t, processed.ExecutionError)
	encoded, marshalErr := json.Marshal(processed)
	require.NoError(t, marshalErr)
	require.LessOrEqual(t, len(encoded), limit)
}

func TestResultProcessorReplacementBudgetBoundary(t *testing.T) {
	replacement := ProcessedResult{
		Value:     safeOmittedResultValue(),
		Truncated: true,
	}
	replacementJSON, err := json.Marshal(replacement)
	require.NoError(t, err)
	limit := len(replacementJSON)

	processor := mustResultProcessor(t, int64(limit), nil)
	processed, err := processor.Process(
		context.Background(), validResultPreflight(), strings.Repeat("x", 4096), nil,
	)
	require.NoError(t, err)
	encoded, err := json.Marshal(processed)
	require.NoError(t, err)
	require.Len(t, encoded, limit)
	require.True(t, processed.Truncated)

	tinyProcessor := mustResultProcessor(t, int64(limit-1), nil)
	tiny, err := tinyProcessor.Process(
		context.Background(), validResultPreflight(),
		strings.Repeat("do-not-leak-raw-result", 100), nil,
	)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "do-not-leak-raw-result")
	tinyJSON, marshalErr := json.Marshal(tiny)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(tinyJSON), "do-not-leak-raw-result")
}

func TestResultProcessorRejectsLimitsBelowStableMinimum(t *testing.T) {
	minimalJSON, err := json.Marshal(minimalProcessedResult())
	require.NoError(t, err)
	require.Equal(t, minimumProcessedResultJSON, string(minimalJSON))
	minimum := int64(len(minimumProcessedResultJSON))
	require.Equal(t, int64(48), minimum)

	for _, limit := range []int64{1, minimum - 1} {
		guard := mustResultGuard(t, limit)
		processor, constructorErr := NewResultProcessor(guard, nil)
		require.Nil(t, processor)
		require.EqualError(t, constructorErr,
			"tool safety result processor output limit must be at least 48 bytes")
	}

	guard := mustResultGuard(t, minimum)
	processor, err := NewResultProcessor(guard, nil)
	require.NoError(t, err)
	processed, processErr := processor.Process(
		context.Background(), validResultPreflight(),
		strings.Repeat("never-return-this-raw-value", 100), nil,
	)
	require.EqualError(t, processErr,
		"tool safety result processor output limit is too small")
	encoded, marshalErr := json.Marshal(processed)
	require.NoError(t, marshalErr)
	require.Equal(t, minimumProcessedResultJSON, string(encoded))
	require.LessOrEqual(t, int64(len(encoded)), minimum)
	require.NotContains(t, string(encoded), "never-return-this-raw-value")
}

func TestResultProcessorFailsSafelyForNonJSONValues(t *testing.T) {
	cyclic := map[string]any{"safe": "value"}
	cyclic["cycle"] = cyclic
	for name, input := range map[string]any{
		"channel":  make(chan string),
		"function": func() {},
		"cycle":    cyclic,
	} {
		t.Run(name, func(t *testing.T) {
			processor := mustResultProcessor(t, 4096, nil)
			processed, err := processor.Process(
				context.Background(), validResultPreflight(), input, errors.New(resultSecret),
			)
			require.Error(t, err)
			require.Nil(t, processed.Value)
			require.NotContains(t, err.Error(), resultSecret)
			encoded, marshalErr := json.Marshal(processed)
			require.NoError(t, marshalErr)
			require.NotContains(t, string(encoded), resultSecret)
		})
	}
}

func TestResultProcessorFailsSafelyForInvalidConfigurationAndReceiver(t *testing.T) {
	guard := mustResultGuard(t, 4096)
	processor, err := NewResultProcessor(guard, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, processor)

	_, err = NewResultProcessor(nil, nil)
	require.EqualError(t, err, "tool safety result processor guard is nil")

	zeroLimitGuard := mustResultGuard(t, 0)
	_, err = NewResultProcessor(zeroLimitGuard, nil)
	require.EqualError(t, err, "tool safety result processor output limit must be positive")

	_, err = NewResultProcessor(
		guard, nil, WithResultAuditFailureMode(AuditFailureMode("invalid")),
	)
	require.EqualError(t, err, "tool safety result processor audit failure mode is invalid")

	for name, current := range map[string]*ResultProcessor{
		"nil":  nil,
		"zero": {},
	} {
		t.Run(name, func(t *testing.T) {
			processed, processErr := current.Process(
				context.Background(), validResultPreflight(), resultSecret, errors.New(errorSecret),
			)
			require.Error(t, processErr)
			encoded, marshalErr := json.Marshal(processed)
			require.NoError(t, marshalErr)
			require.NotContains(t, string(encoded), resultSecret)
			require.NotContains(t, string(encoded), errorSecret)
			require.NotContains(t, processErr.Error(), resultSecret)
			require.NotContains(t, processErr.Error(), errorSecret)
		})
	}
}

func TestResultProcessorCancelledContextAndInvalidPreflightFailSafely(t *testing.T) {
	sink := &recordingAuditSink{}
	processor := mustResultProcessor(t, 4096, sink)

	processed, err := processor.Process(
		cancelledContext(), validResultPreflight(), resultSecret, errors.New(errorSecret),
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, processed.Value)
	require.Empty(t, sink.snapshot())

	invalid := validResultPreflight()
	invalid.Decision = Decision("invalid")
	processed, err = processor.Process(
		context.Background(), invalid, resultSecret, errors.New(errorSecret),
	)
	require.EqualError(t, err, "tool safety result processor preflight report is invalid")
	encoded, marshalErr := json.Marshal(processed)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(encoded), resultSecret)
	require.NotContains(t, string(encoded), errorSecret)
	require.Empty(t, sink.snapshot())

	invalid = validResultPreflight()
	invalid.Backend = Backend("invalid")
	_, err = processor.Process(context.Background(), invalid, "safe", nil)
	require.EqualError(t, err, "tool safety result processor preflight report is invalid")
}

func TestResultProcessorAcceptsGuardReportWithOmittedBackend(t *testing.T) {
	guard := mustResultGuard(t, 4096)
	processor, err := NewResultProcessor(guard, nil)
	require.NoError(t, err)
	preflight := guard.Scan(Request{Command: "go test ./tool/safety"})
	require.Empty(t, preflight.Backend)

	processed, err := processor.Process(context.Background(), preflight, "safe", nil)

	require.NoError(t, err)
	require.Equal(t, "safe", processed.Value)
}

func TestResultProcessorPostAuditCorrelationAndStatusPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		limit     int64
		result    any
		execErr   error
		want      string
		redacted  bool
		truncated bool
	}{
		{name: "success", limit: 4096, result: "safe", want: "success"},
		{name: "execution error", limit: 4096, result: "safe", execErr: errors.New("failed"), want: "execution_error"},
		{name: "redacted", limit: 4096, result: map[string]string{"token": resultSecret}, want: "redacted", redacted: true},
		{name: "truncated", limit: 180, result: strings.Repeat("x", 4096), want: "truncated", truncated: true},
		{name: "truncated beats redacted", limit: 180, result: map[string]string{"token": resultSecret, "safe": strings.Repeat("x", 4096)}, want: "truncated", redacted: true, truncated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &recordingAuditSink{}
			processor := mustResultProcessor(t, tt.limit, sink)
			preflight := validResultPreflight()

			processed, err := processor.Process(
				context.Background(), preflight, tt.result, tt.execErr,
			)

			require.NoError(t, err)
			require.Equal(t, tt.redacted, processed.Redacted)
			require.Equal(t, tt.truncated, processed.Truncated)
			events := sink.snapshot()
			require.Len(t, events, 1)
			event := events[0]
			require.Equal(t, preflight.SchemaVersion, event.SchemaVersion)
			require.Equal(t, preflight.ScanID, event.ScanID)
			require.Equal(t, "post_execute", event.Stage)
			require.Equal(t, preflight.ToolName, event.ToolName)
			require.Equal(t, preflight.Backend, event.Backend)
			require.Equal(t, preflight.Decision, event.Decision)
			require.Equal(t, preflight.RiskLevel, event.RiskLevel)
			require.Equal(t, preflight.RuleID, event.RuleID)
			require.Equal(t, tt.redacted, event.Redacted)
			require.False(t, event.Intercepted)
			require.Equal(t, tt.want, event.ExecutionStatus)
		})
	}
}

func TestResultProcessorAuditFailureModes(t *testing.T) {
	sinkErr := errors.New("audit unavailable")
	for _, tt := range []struct {
		name    string
		mode    AuditFailureMode
		wantErr bool
	}{
		{name: "best effort", mode: AuditBestEffort},
		{name: "required", mode: AuditRequired, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sink := &recordingAuditSink{err: sinkErr}
			guard := mustResultGuard(t, 4096)
			processor, err := NewResultProcessor(
				guard, sink, WithResultAuditFailureMode(tt.mode),
			)
			require.NoError(t, err)

			processed, processErr := processor.Process(
				context.Background(), validResultPreflight(),
				map[string]string{"password": resultSecret}, errors.New(errorSecret),
			)

			require.NotContains(t, processed.ExecutionError, errorSecret)
			if tt.wantErr {
				require.ErrorIs(t, processErr, sinkErr)
				require.NotContains(t, processErr.Error(), resultSecret)
				require.NotContains(t, processErr.Error(), errorSecret)
			} else {
				require.NoError(t, processErr)
			}
			require.Len(t, sink.snapshot(), 1)
		})
	}

	guard := mustResultGuard(t, 4096)
	processor, err := NewResultProcessor(
		guard, nil, WithResultAuditFailureMode(AuditRequired),
	)
	require.NoError(t, err)
	_, err = processor.Process(context.Background(), validResultPreflight(), "safe", nil)
	require.NoError(t, err)
}

func TestResultProcessorConcurrentJSONLAudit(t *testing.T) {
	var output bytes.Buffer
	sink := NewJSONLAuditSink(&output)
	processor := mustResultProcessor(t, 4096, sink)
	const calls = 32
	var wg sync.WaitGroup
	errs := make(chan error, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := processor.Process(
				context.Background(), validResultPreflight(),
				map[string]any{"index": index, "password": resultSecret}, nil,
			)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	require.Len(t, lines, calls)
	for _, line := range lines {
		var event AuditEvent
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		require.Equal(t, "post_execute", event.Stage)
		require.Equal(t, "redacted", event.ExecutionStatus)
		require.NotContains(t, line, resultSecret)
	}
}

func mustResultProcessor(
	t *testing.T,
	limit int64,
	sink AuditSink,
) *ResultProcessor {
	t.Helper()
	processor, err := NewResultProcessor(mustResultGuard(t, limit), sink)
	require.NoError(t, err)
	return processor
}

func mustResultGuard(t *testing.T, limit int64) *Guard {
	t.Helper()
	policy := DefaultPolicy()
	policy.MaxOutputBytes = limit
	guard, err := NewGuard(policy)
	require.NoError(t, err)
	return guard
}

func validResultPreflight() Report {
	return Report{
		SchemaVersion: 1,
		ScanID:        "scan-result-test",
		Decision:      DecisionAllow,
		RiskLevel:     RiskLow,
		RuleID:        "safety.no_findings",
		ToolName:      "workspace_exec",
		Backend:       BackendWorkspaceExec,
	}
}

func safeOmittedResultValue() map[string]any {
	return map[string]any{
		"status": "omitted",
		"reason": "tool output exceeded the configured safety limit",
	}
}

func TestResultProcessorPublicErrorMessagesDoNotLeakValues(t *testing.T) {
	processor := mustResultProcessor(t, 4096, nil)
	processed, err := processor.Process(
		context.Background(), validResultPreflight(),
		func() { fmt.Print(resultSecret) }, errors.New(errorSecret),
	)
	require.Error(t, err)
	combined := fmt.Sprintf("%+v %v", processed, err)
	require.NotContains(t, combined, resultSecret)
	require.NotContains(t, combined, errorSecret)
}
