//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package deps

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildPlanForSources_PythonPackages(t *testing.T) {
	t.Parallel()

	plan, err := BuildPlanForSources(
		t.TempDir(),
		[]string{"test"},
		[]Source{{
			Name: "test",
			Requires: Requirement{
				Python: []PythonPackage{{
					Module:  "definitely_missing_python_module",
					Package: "definitely-missing-python-package",
				}},
			},
		}},
	)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Steps)
	require.Equal(t, stepKindVenv, plan.Steps[0].Kind)
	require.Equal(t, stepKindPython, plan.Steps[1].Kind)
	require.Contains(
		t,
		plan.Steps[1].CommandLine,
		"definitely-missing-python-package",
	)
}

func TestBuildPlanForSources_SystemPackages(t *testing.T) {
	t.Parallel()

	manager := DetectPackageManager()
	if manager == "" {
		t.Skip("no supported package manager")
	}

	action := InstallAction{
		Kind:    manager,
		Formula: "tool-pkg",
		Bins:    []string{"missing-test-bin"},
	}
	plan, err := BuildPlanForSources(
		t.TempDir(),
		[]string{"test"},
		[]Source{{
			Name: "test",
			Requires: Requirement{
				Bins: []string{"missing-test-bin"},
			},
			Install: []InstallAction{action},
		}},
	)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Steps)
	require.Equal(t, stepKindSystem, plan.Steps[0].Kind)
	require.Contains(t, plan.Steps[0].CommandLine, "tool-pkg")
}
