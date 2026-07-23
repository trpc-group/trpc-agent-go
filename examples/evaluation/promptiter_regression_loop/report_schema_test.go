//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"os"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

func TestReportSchemaAndExampleContract(t *testing.T) {
	schemaBytes, err := os.ReadFile("optimization_report.schema.json")
	require.NoError(t, err)
	require.True(t, json.Valid(schemaBytes))
	var schemaDocument any
	require.NoError(t, json.Unmarshal(schemaBytes, &schemaDocument))
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("https://trpc.group/trpc-agent-go/schemas/promptiter-regression-report-v1alpha1.json", schemaDocument))
	compiled, err := compiler.Compile("https://trpc.group/trpc-agent-go/schemas/promptiter-regression-report-v1alpha1.json")
	require.NoError(t, err)

	reportBytes, err := os.ReadFile("example_output/optimization_report.json")
	require.NoError(t, err)
	require.True(t, json.Valid(reportBytes))
	var report any
	require.NoError(t, json.Unmarshal(reportBytes, &report))
	require.NoError(t, compiled.Validate(report))

	var incomplete map[string]any
	require.NoError(t, json.Unmarshal(reportBytes, &incomplete))
	delete(incomplete, "finalDecision")
	require.Error(t, compiled.Validate(incomplete))
}
