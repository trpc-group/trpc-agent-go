//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/internal/skillprofile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/admin"
)

func TestBuildAdminOptions_WiresRuntimeConfigProvider(t *testing.T) {
	t.Parallel()

	cfgPath := writeAdminRuntimeConfigTestFile(t, "")
	opts := adminRuntimeConfigTestOptions(cfgPath)
	svc := admin.New(
		buildAdminConfig(
			opts,
			agentTypeLLM,
			"instance-1",
			admin.LangfuseStatus{},
			t.TempDir(),
			t.TempDir(),
			time.Unix(0, 0),
			nil,
			admin.Routes{},
			nil,
			nil,
			nil,
			nil,
			"127.0.0.1:8081",
			"http://127.0.0.1:8081",
			nil,
			nil,
			nil,
			nil,
		),
		buildAdminOptions(opts)...,
	)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var status admin.RuntimeConfigStatus
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &status))
	require.True(t, status.Enabled)
	require.Equal(t, cfgPath, status.ConfigPath)
	require.NotEmpty(t, status.Sections)
}

func TestBuildAdminOptions_UsesAdminSourceConfigPathEnv(t *testing.T) {
	sourcePath := writeAdminRuntimeConfigTestFile(
		t,
		"skills:\n  max_loaded_skills: 3\n",
	)
	runtimePath := writeAdminRuntimeConfigTestFile(
		t,
		"skills:\n  max_loaded_skills: 7\n",
	)
	t.Setenv(AdminSourceConfigPathEnvName, sourcePath)

	opts := adminRuntimeConfigTestOptions(runtimePath)
	opts.SkillsMaxLoaded = 7

	provider, ok := buildAdminRuntimeConfigProvider(
		opts,
	).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	status, err := provider.RuntimeConfigStatus()
	require.NoError(t, err)
	require.Equal(t, sourcePath, status.ConfigPath)

	require.NoError(t, provider.SaveRuntimeConfigValue(
		"skills.max_loaded_skills",
		"10",
	))

	sourceData, err := os.ReadFile(sourcePath)
	require.NoError(t, err)
	require.Contains(t, string(sourceData), "max_loaded_skills: 10")

	runtimeData, err := os.ReadFile(runtimePath)
	require.NoError(t, err)
	require.Contains(t, string(runtimeData), "max_loaded_skills: 7")
}

func TestBuildAdminOptions_WithoutConfigPath(t *testing.T) {
	t.Parallel()

	opts := adminRuntimeConfigTestOptions("")
	svc := admin.New(
		buildAdminConfig(
			opts,
			agentTypeLLM,
			"instance-1",
			admin.LangfuseStatus{},
			t.TempDir(),
			t.TempDir(),
			time.Unix(0, 0),
			nil,
			admin.Routes{},
			nil,
			nil,
			nil,
			nil,
			"127.0.0.1:8081",
			"http://127.0.0.1:8081",
			nil,
			nil,
			nil,
			nil,
		),
		buildAdminOptions(opts)...,
	)

	req := httptest.NewRequest(http.MethodGet, "/overview", nil)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.NotContains(t, rr.Body.String(), `href="config"`)
}

func TestAdminRuntimeConfigProvider_StatusSaveReset(t *testing.T) {
	t.Parallel()

	cfgPath := writeAdminRuntimeConfigTestFile(
		t,
		"skills:\n  max_loaded_skills: 4\n",
	)
	opts := adminRuntimeConfigTestOptions(cfgPath)
	opts.SkillsMaxLoaded = 4

	provider, ok := buildAdminRuntimeConfigProvider(opts).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	status, err := provider.RuntimeConfigStatus()
	require.NoError(t, err)
	field := findAdminRuntimeConfigField(
		t,
		status,
		"skills.max_loaded_skills",
	)
	require.Equal(t, "4", field.ConfiguredValue)
	require.Equal(t, "explicit", field.ConfiguredSource)
	require.Equal(t, "4", field.RuntimeValue)
	require.False(t, field.PendingRestart)
	require.True(t, field.Resettable)

	require.NoError(t, provider.SaveRuntimeConfigValue(
		"skills.max_loaded_skills",
		"10",
	))

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "max_loaded_skills: 10")

	status, err = provider.RuntimeConfigStatus()
	require.NoError(t, err)
	field = findAdminRuntimeConfigField(
		t,
		status,
		"skills.max_loaded_skills",
	)
	require.Equal(t, "10", field.ConfiguredValue)
	require.Equal(t, "4", field.RuntimeValue)
	require.True(t, field.PendingRestart)

	require.NoError(t, provider.ResetRuntimeConfigValue(
		"skills.max_loaded_skills",
	))

	data, err = os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NotContains(t, string(data), "max_loaded_skills")

	status, err = provider.RuntimeConfigStatus()
	require.NoError(t, err)
	field = findAdminRuntimeConfigField(
		t,
		status,
		"skills.max_loaded_skills",
	)
	require.Empty(t, field.ConfiguredValue)
	require.Equal(t, "inherited", field.ConfiguredSource)
	require.False(t, field.PendingRestart)
	require.False(t, field.Resettable)
}

func TestAdminRuntimeConfigProvider_CodeExecutorSandboxFields(t *testing.T) {
	t.Parallel()

	cfgPath := writeAdminRuntimeConfigTestFile(
		t,
		""+
			"tools:\n"+
			"  code_executor:\n"+
			"    type: sandbox\n"+
			"    auto_execute_code_blocks: true\n"+
			"    sandbox:\n"+
			"      workspace_root: /tmp/openclaw-sandbox\n"+
			"      profile: read_only\n"+
			"      network: enabled\n"+
			"      default_timeout: 45s\n"+
			"      output_max_bytes: 2048\n"+
			"      shell_env:\n"+
			"        inherit: all\n"+
			"        apply_default_excludes: false\n",
	)
	autoExecute := true
	opts := adminRuntimeConfigTestOptions(cfgPath)
	opts.CodeExecutor = codeExecutorOptions{
		Type:                  codeExecutorTypeSandbox,
		AutoExecuteCodeBlocks: &autoExecute,
		Sandbox: sandboxCodeExecutorOptions{
			WorkspaceRoot:  "/tmp/openclaw-sandbox",
			Profile:        sandboxProfileReadOnly,
			Network:        sandboxNetworkEnabled,
			DefaultTimeout: 45 * time.Second,
			OutputMaxBytes: 2048,
			ShellEnv: sandboxShellEnvOptions{
				Inherit:              sandboxShellEnvInheritAll,
				ApplyDefaultExcludes: false,
			},
		},
	}

	provider, ok := buildAdminRuntimeConfigProvider(opts).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	status, err := provider.RuntimeConfigStatus()
	require.NoError(t, err)
	require.Equal(
		t,
		codeExecutorTypeSandbox,
		findAdminRuntimeConfigField(t, status, "tools.code_executor.type").RuntimeValue,
	)
	typeField := findAdminRuntimeConfigField(t, status, "tools.code_executor.type")
	require.Equal(t, []admin.RuntimeConfigOption{
		{Value: "", Label: "inherit"},
		{Value: codeExecutorTypeSandbox, Label: codeExecutorTypeSandbox},
	}, typeField.Options)
	require.Equal(
		t,
		"45s",
		findAdminRuntimeConfigField(
			t,
			status,
			"tools.code_executor.sandbox.default_timeout",
		).RuntimeValue,
	)
	require.Equal(
		t,
		"false",
		findAdminRuntimeConfigField(
			t,
			status,
			"tools.code_executor.sandbox.shell_env.apply_default_excludes",
		).RuntimeValue,
	)
	_, ok = adminRuntimeConfigFieldSpecByKey(
		"tools.code_executor.sandbox.backend",
	)
	require.False(t, ok)
	workspaceRootField := findAdminRuntimeConfigField(
		t,
		status,
		"tools.code_executor.sandbox.workspace_root",
	)
	require.Equal(
		t,
		"tools.code_executor.type",
		workspaceRootField.VisibleWhen.Key,
	)
	require.Equal(t, codeExecutorTypeSandbox, workspaceRootField.VisibleWhen.Value)
	require.False(t, workspaceRootField.Hidden)
	localExecField := findAdminRuntimeConfigField(
		t,
		status,
		"tools.enable_local_exec",
	)
	require.Contains(t, localExecField.Summary, "Legacy compatibility")
	require.Contains(t, localExecField.Summary, "takes precedence")

	require.NoError(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.sandbox.profile",
		sandboxProfileDisabled,
	))
	status, err = provider.RuntimeConfigStatus()
	require.NoError(t, err)
	field := findAdminRuntimeConfigField(
		t,
		status,
		"tools.code_executor.sandbox.profile",
	)
	require.Equal(t, sandboxProfileDisabled, field.ConfiguredValue)
	require.Equal(t, sandboxProfileReadOnly, field.RuntimeValue)
	require.True(t, field.PendingRestart)

	require.NoError(t, provider.ResetRuntimeConfigValue(
		"tools.code_executor.sandbox.profile",
	))
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NotContains(t, string(data), "profile:")
}

func TestAdminRuntimeConfigProvider_HidesSandboxFieldsForInheritedExecutor(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := writeAdminRuntimeConfigTestFile(t, "")
	opts := adminRuntimeConfigTestOptions(cfgPath)
	provider, ok := buildAdminRuntimeConfigProvider(opts).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	status, err := provider.RuntimeConfigStatus()
	require.NoError(t, err)
	field := findAdminRuntimeConfigField(
		t,
		status,
		"tools.code_executor.sandbox.profile",
	)
	require.True(t, field.Hidden)
	require.Equal(t, "tools.code_executor.type", field.VisibleWhen.Key)
	require.Equal(t, codeExecutorTypeSandbox, field.VisibleWhen.Value)
}

func TestAdminRuntimeConfigProvider_CanonicalizesCodeExecutorTypeSelect(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := writeAdminRuntimeConfigTestFile(
		t,
		""+
			"tools:\n"+
			"  code_executor:\n"+
			"    type: Sandbox\n"+
			"    sandbox:\n"+
			"      profile: read_only\n",
	)
	opts := adminRuntimeConfigTestOptions(cfgPath)
	opts.CodeExecutor = codeExecutorOptions{
		Type: codeExecutorTypeSandbox,
		Sandbox: sandboxCodeExecutorOptions{
			Profile: sandboxProfileReadOnly,
		},
	}
	provider, ok := buildAdminRuntimeConfigProvider(opts).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	status, err := provider.RuntimeConfigStatus()
	require.NoError(t, err)
	typeField := findAdminRuntimeConfigField(
		t,
		status,
		"tools.code_executor.type",
	)
	require.Equal(t, "Sandbox", typeField.ConfiguredValue)
	require.Equal(t, codeExecutorTypeSandbox, typeField.EditorValue)
	require.False(t, typeField.PendingRestart)

	profileField := findAdminRuntimeConfigField(
		t,
		status,
		"tools.code_executor.sandbox.profile",
	)
	require.False(t, profileField.Hidden)

	require.NoError(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.type",
		"Sandbox",
	))
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "type: sandbox")
	require.NotContains(t, string(data), "type: Sandbox")
}

func TestAdminRuntimeConfigProvider_SaveCodeExecutorSandboxCreatesConfig(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "openclaw.yaml")
	opts := adminRuntimeConfigTestOptions(cfgPath)
	provider, ok := buildAdminRuntimeConfigProvider(opts).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	require.NoError(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.sandbox.profile",
		sandboxProfileReadOnly,
	))
	require.NoError(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.sandbox.network",
		sandboxNetworkEnabled,
	))
	require.NoError(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.sandbox.default_timeout",
		"45s",
	))
	require.NoError(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.sandbox.output_max_bytes",
		"2048",
	))
	require.NoError(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.sandbox.shell_env.inherit",
		sandboxShellEnvInheritNone,
	))
	require.NoError(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.sandbox.shell_env.apply_default_excludes",
		"false",
	))

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	text := string(data)
	require.Contains(t, text, "tools:")
	require.Contains(t, text, "code_executor:")
	require.Contains(t, text, "enable_local_exec: false")
	require.NotContains(t, text, "enable_openclaw_tools:")
	require.Contains(t, text, "type: sandbox")
	require.Contains(t, text, "sandbox:")
	require.Contains(t, text, "profile: read_only")
	require.Contains(t, text, "network: enabled")
	require.Contains(t, text, "default_timeout: 45s")
	require.Contains(t, text, "output_max_bytes: 2048")
	require.Contains(t, text, "shell_env:")
	require.Contains(t, text, "inherit: none")
	require.Contains(t, text, "apply_default_excludes: false")

	parsedOpts := adminRuntimeConfigTestOptions(cfgPath)
	cfg, err := loadConfigFile(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NoError(t, cfg.apply(&parsedOpts, map[string]struct{}{}))
	require.False(t, parsedOpts.EnableLocalExec)
	require.True(t, parsedOpts.EnableOpenClawTools)
	require.Equal(t, codeExecutorTypeSandbox, parsedOpts.CodeExecutor.Type)
	require.Equal(t, sandboxProfileReadOnly, parsedOpts.CodeExecutor.Sandbox.Profile)
	require.Equal(t, sandboxNetworkEnabled, parsedOpts.CodeExecutor.Sandbox.Network)
	require.Equal(t, 45*time.Second, parsedOpts.CodeExecutor.Sandbox.DefaultTimeout)
	require.Equal(t, 2048, parsedOpts.CodeExecutor.Sandbox.OutputMaxBytes)
	require.Equal(
		t,
		sandboxShellEnvInheritNone,
		parsedOpts.CodeExecutor.Sandbox.ShellEnv.Inherit,
	)
	require.False(t, parsedOpts.CodeExecutor.Sandbox.ShellEnv.ApplyDefaultExcludes)
}

func TestAdminRuntimeConfigProvider_CodeExecutorTypeInheritCleansSandbox(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := writeAdminRuntimeConfigTestFile(
		t,
		""+
			"tools:\n"+
			"  enable_local_exec: false\n"+
			"  enable_openclaw_tools: false\n"+
			"  code_executor:\n"+
			"    type: sandbox\n"+
			"    sandbox:\n"+
			"      profile: read_only\n",
	)
	opts := adminRuntimeConfigTestOptions(cfgPath)
	opts.CodeExecutor = codeExecutorOptions{
		Type: codeExecutorTypeSandbox,
		Sandbox: sandboxCodeExecutorOptions{
			Profile: sandboxProfileReadOnly,
		},
	}
	provider, ok := buildAdminRuntimeConfigProvider(opts).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	require.NoError(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.type",
		"",
	))
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	text := string(data)
	require.Contains(t, text, "enable_local_exec: false")
	require.Contains(t, text, "enable_openclaw_tools: false")
	require.NotContains(t, text, "type:")
	require.NotContains(t, text, "sandbox:")
	require.NotContains(t, text, "profile:")

	parsedOpts := adminRuntimeConfigTestOptions(cfgPath)
	cfg, err := loadConfigFile(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NoError(t, cfg.apply(&parsedOpts, map[string]struct{}{}))
	require.Empty(t, parsedOpts.CodeExecutor.Type)
}

func TestAdminRuntimeConfigProvider_CodeExecutorAutoExecutePreservesLegacyLocal(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "openclaw.yaml")
	opts := adminRuntimeConfigTestOptions(cfgPath)
	opts.EnableLocalExec = true
	provider, ok := buildAdminRuntimeConfigProvider(opts).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	require.NoError(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.auto_execute_code_blocks",
		"false",
	))
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	text := string(data)
	require.Contains(t, text, "code_executor:")
	require.Contains(t, text, "auto_execute_code_blocks: false")
	require.NotContains(t, text, "type:")

	parsedOpts := adminRuntimeConfigTestOptions(cfgPath)
	parsedOpts.EnableLocalExec = true
	cfg, err := loadConfigFile(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NoError(t, cfg.apply(&parsedOpts, map[string]struct{}{}))
	require.Empty(t, parsedOpts.CodeExecutor.Type)
	require.NotNil(t, parsedOpts.CodeExecutor.AutoExecuteCodeBlocks)
	require.False(t, *parsedOpts.CodeExecutor.AutoExecuteCodeBlocks)
	exec, err := codeExecutorFromConfig(
		t.TempDir(),
		parsedOpts.EnableLocalExec,
		parsedOpts.CodeExecutor,
	)
	require.NoError(t, err)
	require.NotNil(t, exec)
}

func TestAdminRuntimeConfigProvider_ResetCodeExecutorTypeCleansSandbox(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := writeAdminRuntimeConfigTestFile(
		t,
		""+
			"tools:\n"+
			"  enable_local_exec: true\n"+
			"  code_executor:\n"+
			"    type: sandbox\n"+
			"    sandbox:\n"+
			"      profile: read_only\n",
	)
	opts := adminRuntimeConfigTestOptions(cfgPath)
	opts.EnableLocalExec = true
	opts.CodeExecutor = codeExecutorOptions{
		Type: codeExecutorTypeSandbox,
		Sandbox: sandboxCodeExecutorOptions{
			Profile: sandboxProfileReadOnly,
		},
	}
	provider, ok := buildAdminRuntimeConfigProvider(opts).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	require.NoError(t, provider.ResetRuntimeConfigValue(
		"tools.code_executor.type",
	))
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	text := string(data)
	require.NotContains(t, text, "type:")
	require.NotContains(t, text, "sandbox:")

	parsedOpts := adminRuntimeConfigTestOptions(cfgPath)
	cfg, err := loadConfigFile(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NoError(t, cfg.apply(&parsedOpts, map[string]struct{}{}))
	require.Empty(t, parsedOpts.CodeExecutor.Type)
	exec, err := codeExecutorFromConfig(
		t.TempDir(),
		parsedOpts.EnableLocalExec,
		parsedOpts.CodeExecutor,
	)
	require.NoError(t, err)
	require.NotNil(t, exec)
}

func TestAdminRuntimeConfigProvider_SaveBoolFieldCreatesConfig(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "openclaw.yaml")
	opts := adminRuntimeConfigTestOptions(cfgPath)
	provider, ok := buildAdminRuntimeConfigProvider(opts).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	require.NoError(t, provider.SaveRuntimeConfigValue(
		"admin.auto_port",
		"false",
	))

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "admin:")
	require.Contains(t, string(data), "auto_port: false")
}

func TestAdminRuntimeConfigProvider_SaveStringFieldAndEnvExpansion(
	t *testing.T,
) {
	t.Setenv("OPENCLAW_TEST_BASE_URL", "https://runtime.example")
	cfgPath := writeAdminRuntimeConfigTestFile(
		t,
		"model:\n  base_url: ${OPENCLAW_TEST_BASE_URL}\n",
	)
	opts := adminRuntimeConfigTestOptions(cfgPath)
	opts.OpenAIBaseURL = "https://runtime.example"
	provider, ok := buildAdminRuntimeConfigProvider(opts).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	status, err := provider.RuntimeConfigStatus()
	require.NoError(t, err)
	field := findAdminRuntimeConfigField(
		t,
		status,
		"model.base_url",
	)
	require.Equal(t, "${OPENCLAW_TEST_BASE_URL}", field.ConfiguredValue)
	require.False(t, field.PendingRestart)

	require.NoError(t, provider.SaveRuntimeConfigValue(
		"model.base_url",
		"https://override.example",
	))
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "base_url: https://override.example")
}

func TestAdminRuntimeConfigProvider_ErrorPaths(t *testing.T) {
	t.Parallel()

	var nilProvider *adminRuntimeConfigProvider
	status, err := nilProvider.RuntimeConfigStatus()
	require.NoError(t, err)
	require.Equal(t, admin.RuntimeConfigStatus{}, status)
	require.Error(t, nilProvider.SaveRuntimeConfigValue("admin.auto_port", "true"))
	require.Error(t, nilProvider.ResetRuntimeConfigValue("admin.auto_port"))

	cfgPath := writeAdminRuntimeConfigTestFile(t, "")
	opts := adminRuntimeConfigTestOptions(cfgPath)
	provider, ok := buildAdminRuntimeConfigProvider(opts).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	require.Error(t, provider.SaveRuntimeConfigValue("missing", "value"))
	require.Error(t, provider.ResetRuntimeConfigValue("missing"))
	require.Error(t, provider.SaveRuntimeConfigValue("admin.auto_port", "maybe"))
	require.Error(t, provider.SaveRuntimeConfigValue("skills.max_loaded_skills", "nan"))
	require.Error(t, provider.SaveRuntimeConfigValue("skills.tool_profile", "invalid"))
	require.Error(t, provider.SaveRuntimeConfigValue("skills.load_mode", "invalid"))
	require.Error(t, provider.SaveRuntimeConfigValue("tools.code_executor.type", "remote"))
	require.Error(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.sandbox.profile",
		"everything",
	))
	require.Error(t, provider.SaveRuntimeConfigValue(
		"tools.code_executor.sandbox.network",
		"egress-only",
	))

	badPath := writeAdminRuntimeConfigTestFile(
		t,
		"first: 1\n---\nsecond: 2\n",
	)
	badProvider, ok := buildAdminRuntimeConfigProvider(
		adminRuntimeConfigTestOptions(badPath),
	).(*adminRuntimeConfigProvider)
	require.True(t, ok)
	_, err = badProvider.RuntimeConfigStatus()
	require.Error(t, err)
	require.Error(t, badProvider.SaveRuntimeConfigValue("admin.addr", "x"))
	require.Error(t, badProvider.ResetRuntimeConfigValue("admin.addr"))
}

func TestAdminRuntimeConfigHelpers(t *testing.T) {
	t.Parallel()

	spec, ok := adminRuntimeConfigFieldSpecByKey("missing")
	require.False(t, ok)
	require.Equal(t, adminRuntimeConfigFieldSpec{}, spec)
	require.Equal(t, "", adminRuntimePreferredKey(adminRuntimeKey("")))

	require.Equal(
		t,
		"true",
		adminRuntimeComparableConfiguredValue(
			adminRuntimeConfigFieldSpec{ValueType: adminRuntimeConfigValueBool},
			"true",
		),
	)
	require.Equal(
		t,
		"12",
		adminRuntimeComparableConfiguredValue(
			adminRuntimeConfigFieldSpec{ValueType: adminRuntimeConfigValueInt},
			"12",
		),
	)
	require.Equal(
		t,
		"bad-bool",
		adminRuntimeComparableConfiguredValue(
			adminRuntimeConfigFieldSpec{ValueType: adminRuntimeConfigValueBool},
			"bad-bool",
		),
	)
	require.Equal(
		t,
		"bad-int",
		adminRuntimeComparableConfiguredValue(
			adminRuntimeConfigFieldSpec{ValueType: adminRuntimeConfigValueInt},
			"bad-int",
		),
	)
	require.Equal(
		t,
		os.ExpandEnv("$HOME/bin"),
		adminRuntimeComparableConfiguredValue(
			adminRuntimeConfigFieldSpec{ValueType: adminRuntimeConfigValueString},
			"$HOME/bin",
		),
	)

	require.True(t, adminRuntimeOptionValueAllowed("turn", nil))
	require.False(
		t,
		adminRuntimeOptionValueAllowed(
			"bad",
			[]admin.RuntimeConfigOption{{Value: "turn"}},
		),
	)

	require.Equal(
		t,
		"fallback",
		adminRuntimePreferredKey(adminRuntimeKey("", "fallback")),
	)
	require.True(
		t,
		adminRuntimeKeyMatches(
			&yaml.Node{Value: "alias"},
			adminRuntimeKey("preferred", "alias"),
		),
	)
	require.False(t, adminRuntimeKeyMatches(nil, adminRuntimeKey("preferred")))
	require.False(
		t,
		adminRuntimeKeyMatches(
			&yaml.Node{Value: "other"},
			adminRuntimeKey("preferred", "alias"),
		),
	)

	_, _, err := adminRuntimeConfigDocumentFromPath("")
	require.Error(t, err)
	doc, rootDoc, err := adminRuntimeConfigDocumentFromPath(
		filepath.Join(t.TempDir(), "missing.yaml"),
	)
	require.NoError(t, err)
	require.Equal(t, yaml.DocumentNode, doc.Kind)
	require.NotNil(t, rootDoc)

	parent := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	require.NoError(t, adminRuntimeSetMappingBool(
		parent,
		adminRuntimeKey("enabled"),
		true,
	))
	require.NoError(t, adminRuntimeSetMappingString(
		parent,
		adminRuntimeKey("base_url"),
		"https://example.test",
	))
	require.Equal(
		t,
		"https://example.test",
		adminRuntimeLookupMappingValue(parent, adminRuntimeKey("base_url")).Value,
	)
	require.NoError(t, adminRuntimeSetMappingString(
		parent,
		adminRuntimeKey("base_url"),
		"https://updated.example",
	))
	require.Equal(
		t,
		"https://updated.example",
		adminRuntimeLookupMappingValue(parent, adminRuntimeKey("base_url")).Value,
	)
	require.NoError(t, adminRuntimeSetMappingInt(
		parent,
		adminRuntimeKey("max_loaded_skills"),
		8,
	))
	require.Equal(
		t,
		"8",
		adminRuntimeLookupMappingValue(
			parent,
			adminRuntimeKey("max_loaded_skills"),
		).Value,
	)
	require.Error(
		t,
		adminRuntimeSetMappingString(
			&yaml.Node{Kind: yaml.SequenceNode},
			adminRuntimeKey("broken"),
			"value",
		),
	)
	require.Error(
		t,
		adminRuntimeSetScalarValue(
			nil,
			adminRuntimeKey("broken"),
			"!!str",
			"value",
		),
	)
	_, err = adminRuntimeEnsureMappingChild(nil, adminRuntimeKey("skills"))
	require.Error(t, err)
	_, err = adminRuntimeEnsureMappingChild(
		&yaml.Node{Kind: yaml.SequenceNode},
		adminRuntimeKey("skills"),
	)
	require.Error(t, err)
	child, err := adminRuntimeEnsureMappingChild(
		parent,
		adminRuntimeKey("settings"),
	)
	require.NoError(t, err)
	require.NotNil(t, child)
	child, err = adminRuntimeEnsureMappingChild(
		parent,
		adminRuntimeKey("settings"),
	)
	require.NoError(t, err)
	require.NotNil(t, child)
	parentWithNilChild := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "empty"},
			nil,
		},
	}
	child, err = adminRuntimeEnsureMappingChild(
		parentWithNilChild,
		adminRuntimeKey("empty"),
	)
	require.NoError(t, err)
	require.NotNil(t, child)
	adminRuntimeDeleteMappingValue(parent, adminRuntimeKey("missing"))
	adminRuntimeDeleteMappingValue(parent, adminRuntimeKey("settings"))
	require.Nil(
		t,
		adminRuntimeLookupMappingValue(parent, adminRuntimeKey("settings")),
	)

	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	fieldPath := []adminRuntimeConfigKeyRef{
		adminRuntimeKey("skills"),
		adminRuntimeKey("max_loaded_skills"),
	}
	fieldParent, err := adminRuntimeEnsureFieldParent(root, fieldPath)
	require.NoError(t, err)
	require.Error(t, adminRuntimeSetFieldValue(
		fieldParent,
		adminRuntimeConfigFieldSpec{
			Key:       "skills.mode",
			InputType: adminRuntimeConfigInputText,
			ValueType: adminRuntimeConfigValueString,
			Path: []adminRuntimeConfigKeyRef{
				adminRuntimeKey("skills"),
				adminRuntimeKey("mode"),
			},
		},
		"",
	))
	require.NoError(t, adminRuntimeSetFieldValue(
		fieldParent,
		adminRuntimeConfigFieldSpec{
			Key:       "skills.max_loaded_skills",
			InputType: adminRuntimeConfigInputNumber,
			ValueType: adminRuntimeConfigValueInt,
			Path:      fieldPath,
		},
		"6",
	))
	require.Equal(
		t,
		"6",
		adminRuntimeFieldNode(root, fieldPath).Value,
	)
	modePath := []adminRuntimeConfigKeyRef{
		adminRuntimeKey("skills"),
		adminRuntimeKey("load_mode"),
	}
	modeParent, err := adminRuntimeEnsureFieldParent(root, modePath)
	require.NoError(t, err)
	require.NoError(t, adminRuntimeSetFieldValue(
		modeParent,
		adminRuntimeConfigFieldSpec{
			Key:       "skills.load_mode",
			InputType: adminRuntimeConfigInputSelect,
			ValueType: adminRuntimeConfigValueString,
			Path:      modePath,
			Options:   []admin.RuntimeConfigOption{{Value: "turn"}},
		},
		"turn",
	))
	require.Equal(t, "turn", adminRuntimeFieldNode(root, modePath).Value)
	adminRuntimeDeleteField(root, fieldPath)
	require.Nil(t, adminRuntimeFieldNode(root, fieldPath))
	adminRuntimeDeleteField(root, modePath)
	adminRuntimeDeleteField(root, []adminRuntimeConfigKeyRef{
		adminRuntimeKey("missing"),
		adminRuntimeKey("field"),
	})
	require.Nil(
		t,
		adminRuntimeFieldParent(
			&yaml.Node{Kind: yaml.SequenceNode},
			fieldPath,
		),
	)
	_, err = adminRuntimeEnsureFieldParent(
		&yaml.Node{Kind: yaml.SequenceNode},
		fieldPath,
	)
	require.Error(t, err)
	_, err = adminRuntimeEnsureFieldParent(
		&yaml.Node{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "skills"},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "bad"},
			},
		},
		fieldPath,
	)
	require.Error(t, err)
}

func TestAdminRuntimeConfigHelperErrorBranches(t *testing.T) {
	t.Parallel()

	badPath := writeAdminRuntimeConfigTestFile(
		t,
		"first: 1\n---\nsecond: 2\n",
	)
	_, _, err := adminRuntimeConfigDocumentFromPath(badPath)
	require.Error(t, err)

	provider := &adminRuntimeConfigProvider{}
	require.Error(t, provider.SaveRuntimeConfigValue("admin.addr", "x"))
	require.Error(t, provider.ResetRuntimeConfigValue("admin.addr"))

	_, err = adminRuntimeEnsureFieldParent(nil, []adminRuntimeConfigKeyRef{
		adminRuntimeKey("skills"),
		adminRuntimeKey("watch"),
	})
	require.Error(t, err)

	badParent := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "skills"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "not-a-map"},
		},
	}
	_, err = adminRuntimeEnsureMappingChild(
		badParent,
		adminRuntimeKey("skills"),
	)
	require.Error(t, err)

	require.Nil(
		t,
		adminRuntimeLookupMappingValue(
			&yaml.Node{Kind: yaml.SequenceNode},
			adminRuntimeKey("skills"),
		),
	)
	adminRuntimeDeleteMappingValue(
		&yaml.Node{Kind: yaml.SequenceNode},
		adminRuntimeKey("skills"),
	)

	require.Error(
		t,
		adminRuntimeSetMappingBool(nil, adminRuntimeKey("enabled"), true),
	)
	require.Error(
		t,
		adminRuntimeSetScalarValue(
			&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "enabled"},
				{Kind: yaml.SequenceNode},
			}},
			adminRuntimeKey("enabled"),
			"!!bool",
			"true",
		),
	)
	require.False(
		t,
		adminRuntimeKeyMatches(&yaml.Node{Value: ""}, adminRuntimeKey("enabled")),
	)
	require.Equal(
		t,
		1,
		len(adminRuntimeStringOptions("", "turn")),
	)
	boolPath := []adminRuntimeConfigKeyRef{
		adminRuntimeKey("admin"),
		adminRuntimeKey("enabled"),
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent, err := adminRuntimeEnsureFieldParent(root, boolPath)
	require.NoError(t, err)
	require.NoError(t, adminRuntimeSetFieldValue(
		parent,
		adminRuntimeConfigFieldSpec{
			Key:       "admin.enabled",
			InputType: adminRuntimeConfigInputSelect,
			ValueType: adminRuntimeConfigValueBool,
			Path:      boolPath,
			Options: []admin.RuntimeConfigOption{
				{Value: "true"},
				{Value: "false"},
			},
		},
		"true",
	))
	require.Equal(t, "true", adminRuntimeFieldNode(root, boolPath).Value)

}

func TestAdminWritableConfigPath(t *testing.T) {
	t.Setenv(AdminSourceConfigPathEnvName, "  /tmp/source.yaml ")
	require.Equal(
		t,
		"/tmp/source.yaml",
		adminWritableConfigPath("/tmp/runtime.yaml"),
	)
	t.Setenv(AdminSourceConfigPathEnvName, " ")
	require.Equal(
		t,
		"/tmp/runtime.yaml",
		adminWritableConfigPath(" /tmp/runtime.yaml "),
	)
}

func TestRuntimeAdminOptionsHelpers(t *testing.T) {
	t.Parallel()

	var nilRuntime *Runtime
	require.Nil(t, runtimeAdminOptions(nilRuntime))
	setRuntimeAdminOptions(nilRuntime, buildAdminOptions(runOptions{}))
	clearRuntimeAdminOptions(nilRuntime)

	rt := &Runtime{}
	require.Nil(t, runtimeAdminOptions(rt))

	runtimeAdminOptionsStore.Store(rt, "bad")
	require.Nil(t, runtimeAdminOptions(rt))

	runtimeAdminOptionsStore.Store(rt, []admin.Option{})
	require.Nil(t, runtimeAdminOptions(rt))

	opts := buildAdminOptions(
		adminRuntimeConfigTestOptions(
			writeAdminRuntimeConfigTestFile(t, ""),
		),
	)
	setRuntimeAdminOptions(rt, opts)
	got := runtimeAdminOptions(rt)
	require.Len(t, got, len(opts))
	require.NotNil(t, got[0])
	clearRuntimeAdminOptions(rt)
	require.Nil(t, runtimeAdminOptions(rt))

	setRuntimeAdminOptions(rt, nil)
	require.Nil(t, runtimeAdminOptions(rt))
}

func adminRuntimeConfigTestOptions(configPath string) runOptions {
	return runOptions{
		ConfigPath:           strings.TrimSpace(configPath),
		AdminEnabled:         true,
		AdminAddr:            defaultAdminAddr,
		AdminAutoPort:        defaultAdminAutoPort,
		ModelMode:            modeOpenAI,
		OpenAIVariant:        defaultOpenAIVariant,
		SkillsWatch:          true,
		SkillsWatchDebounce:  defaultSkillsWatchDebounce,
		SkillsToolProfile:    defaultSkillsToolProfile,
		SkillsLoadMode:       defaultSkillsLoadMode,
		SkillsMaxLoaded:      0,
		SkillsToolResults:    true,
		SkillsSkipFallback:   true,
		SessionBackend:       sessionBackendInMemory,
		MemoryBackend:        memoryBackendInMemory,
		EnableOpenClawTools:  true,
		EnableParallelTools:  false,
		RefreshToolSetsOnRun: false,
	}
}

func writeAdminRuntimeConfigTestFile(
	t *testing.T,
	body string,
) string {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "openclaw.yaml")
	require.NoError(t, os.WriteFile(
		cfgPath,
		[]byte(body),
		0o600,
	))
	return cfgPath
}

func findAdminRuntimeConfigField(
	t *testing.T,
	status admin.RuntimeConfigStatus,
	key string,
) admin.RuntimeConfigField {
	t.Helper()

	for _, section := range status.Sections {
		for _, field := range section.Fields {
			if field.Key == key {
				return field
			}
		}
	}
	t.Fatalf("runtime config field %q not found", key)
	return admin.RuntimeConfigField{}
}

func TestBuildAdminOptions_ExposesSkillsToolProfileField(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := writeAdminRuntimeConfigTestFile(
		t,
		"skills:\n  toolProfile: knowledge_only\n",
	)
	opts := adminRuntimeConfigTestOptions(cfgPath)
	opts.SkillsToolProfile = skillprofile.KnowledgeOnly

	provider, ok := buildAdminRuntimeConfigProvider(
		opts,
	).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	status, err := provider.RuntimeConfigStatus()
	require.NoError(t, err)
	field := findAdminRuntimeConfigField(
		t,
		status,
		"skills.tool_profile",
	)
	require.Equal(t, skillprofile.KnowledgeOnly, field.RuntimeValue)
	require.Equal(
		t,
		skillprofile.KnowledgeOnly,
		field.ConfiguredValue,
	)
	require.Equal(
		t,
		adminRuntimeConfigInputSelect,
		field.InputType,
	)
	require.Len(t, field.Options, 2)
	require.Equal(
		t,
		skillprofile.KnowledgeOnly,
		field.Options[0].Value,
	)
	require.Equal(
		t,
		skillprofile.Full,
		field.Options[1].Value,
	)
}

func TestBuildAdminOptions_ExposesOpenClawToolingGuidanceField(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := writeAdminRuntimeConfigTestFile(
		t,
		"tools:\n  openclaw_tooling_guidance: \"\"\n",
	)
	opts := adminRuntimeConfigTestOptions(cfgPath)
	guide := ""
	opts.OpenClawToolingGuide = &guide

	provider, ok := buildAdminRuntimeConfigProvider(
		opts,
	).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	status, err := provider.RuntimeConfigStatus()
	require.NoError(t, err)
	field := findAdminRuntimeConfigField(
		t,
		status,
		"tools.openclaw_tooling_guidance",
	)
	require.Equal(t, "", field.RuntimeValue)
	require.Equal(t, "", field.ConfiguredValue)
	require.Equal(
		t,
		adminRuntimeConfigInputText,
		field.InputType,
	)
}

func TestBuildAdminOptions_ExposesDeferredToolSurfaceFields(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := writeAdminRuntimeConfigTestFile(
		t,
		"tools:\n"+
			"  defer_to_dynamic_agent_mode: auto\n"+
			"  defer_to_dynamic_agent_threshold_chars: 1234\n"+
			"  dynamic_agent_timeout: 3m\n"+
			"  defer_direct_tools: [exec_command]\n"+
			"  defer_default_direct_tools: false\n",
	)
	opts := adminRuntimeConfigTestOptions(cfgPath)
	opts.DeferToolSurfaceMode = deferToolSurfaceModeAuto
	opts.DeferToolSurfaceChars = 1234
	opts.DeferToolSurfaceDirect = "exec_command"
	opts.DeferToolSurfaceDefaultDirectTools = false
	opts.DynamicAgentTimeout = 3 * time.Minute

	provider, ok := buildAdminRuntimeConfigProvider(
		opts,
	).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	status, err := provider.RuntimeConfigStatus()
	require.NoError(t, err)
	mode := findAdminRuntimeConfigField(
		t,
		status,
		"tools.defer_to_dynamic_agent_mode",
	)
	require.Equal(t, deferToolSurfaceModeAuto, mode.RuntimeValue)
	require.Equal(t, deferToolSurfaceModeAuto, mode.ConfiguredValue)
	threshold := findAdminRuntimeConfigField(
		t,
		status,
		"tools.defer_to_dynamic_agent_threshold_chars",
	)
	require.Equal(t, "1234", threshold.RuntimeValue)
	require.Equal(t, "1234", threshold.ConfiguredValue)
	timeout := findAdminRuntimeConfigField(
		t,
		status,
		"tools.dynamic_agent_timeout",
	)
	require.Equal(t, "3m0s", timeout.RuntimeValue)
	require.Equal(t, "3m", timeout.ConfiguredValue)
	direct := findAdminRuntimeConfigField(
		t,
		status,
		"tools.defer_direct_tools",
	)
	require.Equal(t, "exec_command", direct.RuntimeValue)
	defaultDirect := findAdminRuntimeConfigField(
		t,
		status,
		"tools.defer_default_direct_tools",
	)
	require.Equal(t, "false", defaultDirect.RuntimeValue)
	require.Equal(t, "false", defaultDirect.ConfiguredValue)
}

func TestBuildAdminOptions_ExposesAgentBudgetFields(t *testing.T) {
	t.Parallel()

	cfgPath := writeAdminRuntimeConfigTestFile(
		t,
		"agent:\n  max_llm_calls: 7\n  max_tool_iterations: 12\n",
	)
	opts := adminRuntimeConfigTestOptions(cfgPath)
	opts.MaxLLMCalls = 7
	opts.MaxToolIterations = 12

	provider, ok := buildAdminRuntimeConfigProvider(
		opts,
	).(*adminRuntimeConfigProvider)
	require.True(t, ok)

	status, err := provider.RuntimeConfigStatus()
	require.NoError(t, err)
	llmCalls := findAdminRuntimeConfigField(
		t,
		status,
		"agent.max_llm_calls",
	)
	require.Equal(t, "7", llmCalls.RuntimeValue)
	require.Equal(t, "7", llmCalls.ConfiguredValue)
	field := findAdminRuntimeConfigField(
		t,
		status,
		"agent.max_tool_iterations",
	)
	require.Equal(t, "12", field.RuntimeValue)
	require.Equal(t, "12", field.ConfiguredValue)
}
