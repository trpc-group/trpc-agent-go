//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestOutputSpec_UnmarshalJSON_SnakeCase(t *testing.T) {
	var spec codeexecutor.OutputSpec
	data := []byte(`{
  "globs": ["out/*.txt"],
  "max_files": 7,
  "max_file_bytes": 11,
  "max_total_bytes": 13,
  "save": true,
  "name_template": "pref/",
  "inline": true
}`)
	err := json.Unmarshal(data, &spec)
	assert.NoError(t, err)
	assert.Equal(t, []string{"out/*.txt"}, spec.Globs)
	assert.Equal(t, 7, spec.MaxFiles)
	assert.Equal(t, int64(11), spec.MaxFileBytes)
	assert.Equal(t, int64(13), spec.MaxTotalBytes)
	assert.True(t, spec.Save)
	assert.Equal(t, "pref/", spec.NameTemplate)
	assert.True(t, spec.Inline)
}

func TestOutputSpec_UnmarshalJSON_SnakeOverridesLegacy(t *testing.T) {
	var spec codeexecutor.OutputSpec
	data := []byte(`{
  "MaxFiles": 1,
  "max_files": 2
}`)
	err := json.Unmarshal(data, &spec)
	assert.NoError(t, err)
	assert.Equal(t, 2, spec.MaxFiles)
}

func TestOutputSpec_UnmarshalJSON_InvalidJSON(t *testing.T) {
	var spec codeexecutor.OutputSpec
	err := json.Unmarshal([]byte("{"), &spec)
	assert.Error(t, err)
}

func TestOutputSpec_UnmarshalJSON_InvalidSnakeTypes(t *testing.T) {
	var spec codeexecutor.OutputSpec
	err := json.Unmarshal([]byte(`{"max_files":"bad"}`), &spec)
	assert.Error(t, err)
}
