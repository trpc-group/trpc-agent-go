//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEvalSetRejectsMisspelledSafetyFields(t *testing.T) {
	tests := []struct {
		name     string
		caseBody string
		unknown  string
	}{
		{
			name: "critical",
			caseBody: `
        "criticality": true,
        "expectations": {},`,
			unknown: "criticality",
		},
		{
			name: "required_facts",
			caseBody: `
        "critical": true,
        "expectations": {"requiredFact": ["fact"]},`,
			unknown: "requiredFact",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "set.json")
			content := `{
  "evalSetId": "validation",
  "evalCases": [{
    "evalId": "critical-case",` + test.caseBody + `
    "conversation": [{"finalResponse": {"role": "assistant", "content": "ok"}}],
    "fakeResponses": {"baseline": {"response": "ok", "usage": {}}}
  }]
}`
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
			_, err := LoadEvalSet(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.unknown)
		})
	}
}

func TestLoadConfigAcceptsTraceReplayMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "promptiter.json")
	content := `{
  "schemaVersion": "1.0",
  "mode": "trace",
  "seed": 1,
  "maxRounds": 1,
  "surface": {"structureId": "s", "nodeId": "n", "type": "instruction"},
  "candidates": [{"id": "candidate", "appendPrompt": "change", "reason": "test"}],
  "gate": {"minValidationScoreGain": 0},
  "fakeEngine": {"name": "fake", "version": "1", "fallbackVariant": "baseline"}
}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	config, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "trace", config.Mode)
}

func TestLoadEvalSetPreservesExplicitZeroPassThreshold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "set.json")
	content := `{
  "evalSetId": "zero-threshold",
  "passThreshold": 0,
  "evalCases": [{
    "evalId": "case",
    "conversation": [{"finalResponse": {"role": "assistant", "content": "ok"}}],
    "fakeResponses": {"baseline": {"response": "ok", "usage": {}}}
  }]
}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	set, err := LoadEvalSet(path)
	require.NoError(t, err)
	require.NotNil(t, set.PassThreshold)
	assert.Zero(t, *set.PassThreshold)
}

func TestLoadEvalSetRejectsDuplicateJSONKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "set.json")
	content := `{
  "evalSetId": "duplicates",
  "evalCases": [{
    "evalId": "case",
    "critical": true,
    "critical": false,
    "conversation": [{"finalResponse": {"role": "assistant", "content": "ok"}}],
    "fakeResponses": {"baseline": {"response": "ok", "usage": {}}}
  }]
}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	_, err := LoadEvalSet(path)
	require.ErrorContains(t, err, `duplicate key "critical"`)
}

func TestLoadEvalSetRejectsInvalidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "set.json")
	content := append([]byte(`{"evalSetId":"`), 0xff)
	content = append(content, []byte(`"}`)...)
	require.NoError(t, os.WriteFile(path, content, 0o600))
	_, err := LoadEvalSet(path)
	require.ErrorContains(t, err, "not valid UTF-8")
}

func TestLoadEvalSetRejectsInvalidPromptSemanticHash(t *testing.T) {
	tests := []struct {
		name string
		hash string
	}{
		{name: "too short", hash: "abc123"},
		{name: "uppercase", hash: strings.Repeat("A", 64)},
		{name: "non hexadecimal", hash: strings.Repeat("g", 64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "set.json")
			content := fmt.Sprintf(`{
  "evalSetId": "invalid-hash",
  "evalCases": [{
    "evalId": "case",
    "conversation": [{"finalResponse": {"role": "assistant", "content": "ok"}}],
    "fakeResponses": {
      "candidate": {
        "response": "ok",
        "promptSemanticSha256": %q,
        "usage": {}
      }
    }
  }]
}`, test.hash)
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
			_, err := LoadEvalSet(path)
			require.ErrorContains(t, err, "must be 64 lowercase hexadecimal characters")
		})
	}
}
