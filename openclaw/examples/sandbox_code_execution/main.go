//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main runs OpenClaw-style sandbox code executor scenarios.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/app"
)

const (
	defaultScenario   = "basic-python"
	defaultConfigPath = "examples/sandbox_code_execution/openclaw.yaml"
	appName           = "openclaw-sandbox-code-execution"
)

var errSkip = errors.New("skip")

type scenario struct {
	name string
	run  func(context.Context, exampleConfig, bool) error
}

type exampleConfig struct {
	ConfigPath string      `yaml:"-"`
	AppName    string      `yaml:"app_name,omitempty"`
	StateDir   string      `yaml:"state_dir,omitempty"`
	Model      modelConfig `yaml:"model,omitempty"`
	Tools      toolsConfig `yaml:"tools,omitempty"`
}

type modelConfig struct {
	Mode          string `yaml:"mode,omitempty"`
	Name          string `yaml:"name,omitempty"`
	OpenAIVariant string `yaml:"openai_variant,omitempty"`
}

type toolsConfig struct {
	CodeExecutor codeExecutorConfig `yaml:"code_executor,omitempty"`
}

type codeExecutorConfig struct {
	Type                  string                `yaml:"type,omitempty"`
	AutoExecuteCodeBlocks *bool                 `yaml:"auto_execute_code_blocks,omitempty"`
	Sandbox               sandboxExecutorConfig `yaml:"sandbox,omitempty"`
}

type sandboxExecutorConfig struct {
	WorkspaceRoot  string                `yaml:"workspace_root,omitempty"`
	Backend        string                `yaml:"backend,omitempty"`
	Profile        string                `yaml:"profile,omitempty"`
	Network        string                `yaml:"network,omitempty"`
	DefaultTimeout string                `yaml:"default_timeout,omitempty"`
	OutputMaxBytes int                   `yaml:"output_max_bytes,omitempty"`
	ShellEnv       sandboxShellEnvConfig `yaml:"shell_env,omitempty"`
}

type sandboxShellEnvConfig struct {
	Inherit              string            `yaml:"inherit,omitempty"`
	ApplyDefaultExcludes *bool             `yaml:"apply_default_excludes,omitempty"`
	Exclude              []string          `yaml:"exclude,omitempty"`
	IncludeOnly          []string          `yaml:"include_only,omitempty"`
	Set                  map[string]string `yaml:"set,omitempty"`
}

func main() {
	configPath := flag.String("config", defaultConfigPath, "OpenClaw-style YAML config")
	scenarioName := flag.String("scenario", defaultScenario, "scenario name or all")
	requireOSSandbox := flag.Bool("require-os-sandbox", true, "fail when managed OS sandbox setup is unavailable")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if err := runScenarios(context.Background(), cfg, *scenarioName, *requireOSSandbox); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox OpenClaw example failed: %v\n", err)
		os.Exit(1)
	}
}

func loadConfig(path string) (exampleConfig, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return exampleConfig{}, err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return exampleConfig{}, err
	}
	cfg.ConfigPath = path
	return cfg, nil
}

func defaultConfig() exampleConfig {
	autoExecute := true
	applyDefaultExcludes := true
	return exampleConfig{
		AppName:  appName,
		StateDir: filepath.Join(os.TempDir(), appName),
		Model: modelConfig{
			Name: "glm-4.7-flash",
		},
		Tools: toolsConfig{
			CodeExecutor: codeExecutorConfig{
				Type:                  "sandbox",
				AutoExecuteCodeBlocks: &autoExecute,
				Sandbox: sandboxExecutorConfig{
					Backend:        "auto",
					Profile:        "workspace_write",
					Network:        "restricted",
					DefaultTimeout: "30s",
					OutputMaxBytes: 1 << 20,
					ShellEnv: sandboxShellEnvConfig{
						Inherit:              "core",
						ApplyDefaultExcludes: &applyDefaultExcludes,
					},
				},
			},
		},
	}
}

func runScenarios(
	ctx context.Context,
	cfg exampleConfig,
	scenarioName string,
	requireOSSandbox bool,
) error {
	scenarios := []scenario{
		{"basic-python", runBasicPython},
		{"session-persistence", runSessionPersistence},
		{"env-redaction", runEnvRedaction},
		{"network-restricted", runNetworkRestricted},
		{"timeout", runTimeout},
		{"output-cap", runOutputCap},
		{"workspace-exec-hidden", runWorkspaceExecHidden},
		{"disabled-profile-explicit", runDisabledProfileExplicit},
		{"metadata-protection", runMetadataProtection},
		{"session-id-sanitization", runSessionIDSanitization},
	}
	selected := make(map[string]scenario, len(scenarios))
	for _, sc := range scenarios {
		selected[sc.name] = sc
	}
	var todo []scenario
	if strings.TrimSpace(scenarioName) == "all" {
		todo = scenarios
	} else {
		sc, ok := selected[strings.TrimSpace(scenarioName)]
		if !ok {
			return fmt.Errorf("unknown scenario %q", scenarioName)
		}
		todo = []scenario{sc}
	}
	for _, sc := range todo {
		fmt.Printf("== %s ==\n", sc.name)
		err := sc.run(ctx, cfg, requireOSSandbox)
		switch {
		case err == nil:
			fmt.Printf("PASS %s\n\n", sc.name)
		case errors.Is(err, errSkip):
			fmt.Printf("SKIP %s\n\n", sc.name)
		default:
			return fmt.Errorf("%s: %w", sc.name, err)
		}
	}
	return nil
}

func runBasicPython(
	ctx context.Context,
	cfg exampleConfig,
	requireOSSandbox bool,
) error {
	rt, err := newScenarioRuntime(ctx, cfg, requireOSSandbox)
	if err != nil {
		return err
	}
	defer rt.close()
	text, err := rt.run(
		ctx,
		"basic-python",
		"Return exactly one Python fenced code block and no prose. "+
			"The code must compute count, sum, and mean for "+
			"[5, 12, 8, 15, 7, 9, 11] and print compact JSON with "+
			"keys count, sum, mean.",
	)
	if err != nil {
		return err
	}
	return requireContainsAll(text, `"count"`, "7", `"sum"`, "67", `"mean"`)
}

func runSessionPersistence(
	ctx context.Context,
	cfg exampleConfig,
	requireOSSandbox bool,
) error {
	rt, err := newScenarioRuntime(ctx, cfg, requireOSSandbox)
	if err != nil {
		return err
	}
	defer rt.close()
	sessionID := "session-persistence"
	_, err = rt.runInSession(
		ctx,
		sessionID,
		"Return exactly one Python fenced code block and no prose. "+
			"The code must write the text openclaw-sandbox-ok to "+
			"persist.txt in the current working directory and print WROTE.",
	)
	if err != nil {
		return err
	}
	text, err := rt.runInSession(
		ctx,
		sessionID,
		"Return exactly one Python fenced code block and no prose. "+
			"The code must read persist.txt from the current working "+
			"directory and print its contents.",
	)
	if err != nil {
		return err
	}
	return requireContainsAll(text, "openclaw-sandbox-ok")
}

func runEnvRedaction(
	ctx context.Context,
	cfg exampleConfig,
	requireOSSandbox bool,
) error {
	rt, err := newScenarioRuntime(ctx, cfg, requireOSSandbox)
	if err != nil {
		return err
	}
	defer rt.close()
	text, err := rt.run(
		ctx,
		"env-redaction",
		"Return exactly one Python fenced code block and no prose. "+
			"The code must print JSON booleans for whether "+
			"OPENAI_API_KEY, OPENAI_BASE_URL, and GLM_API_KEY are present "+
			"in os.environ. Do not print any environment variable values.",
	)
	if err != nil {
		return err
	}
	for _, forbidden := range []string{
		`"OPENAI_API_KEY": true`,
		`"OPENAI_BASE_URL": true`,
		`"GLM_API_KEY": true`,
	} {
		if strings.Contains(text, forbidden) {
			return fmt.Errorf("secret-like env was visible: %s", forbidden)
		}
	}
	return nil
}

func runNetworkRestricted(
	ctx context.Context,
	cfg exampleConfig,
	requireOSSandbox bool,
) error {
	rt, err := newScenarioRuntime(ctx, cfg, requireOSSandbox)
	if err != nil {
		return err
	}
	defer rt.close()
	text, err := rt.run(
		ctx,
		"network-restricted",
		"Return exactly one Python fenced code block and no prose. "+
			"The code must try to open https://example.com with a short "+
			"timeout. Print network_unexpected_success if it succeeds; "+
			"otherwise print network_blocked and the exception type.",
	)
	if err != nil {
		return err
	}
	successAt := strings.LastIndex(text, "network_unexpected_success")
	blockedAt := strings.LastIndex(text, "network_blocked")
	if successAt > blockedAt {
		return errors.New("restricted sandbox unexpectedly allowed network")
	}
	return requireContainsAll(text, "network_blocked")
}

func runTimeout(
	ctx context.Context,
	cfg exampleConfig,
	requireOSSandbox bool,
) error {
	cfg.Tools.CodeExecutor.Sandbox.DefaultTimeout = "1s"
	rt, err := newScenarioRuntime(ctx, cfg, requireOSSandbox)
	if err != nil {
		return err
	}
	defer rt.close()
	text, err := rt.run(
		ctx,
		"timeout",
		"Return exactly one Python fenced code block and no prose. "+
			"The code must sleep for 5 seconds, then print too_late.",
	)
	if err != nil {
		return err
	}
	lower := strings.ToLower(text)
	tooLateAt := strings.LastIndex(lower, "too_late")
	timeoutAt := strings.LastIndex(lower, "timeout")
	deadlineAt := strings.LastIndex(lower, "deadline")
	if tooLateAt > timeoutAt && tooLateAt > deadlineAt {
		return errors.New("timeout scenario completed unexpectedly")
	}
	if timeoutAt < 0 && deadlineAt < 0 {
		return fmt.Errorf("timeout was not surfaced clearly: %s", trim(text))
	}
	return nil
}

func runOutputCap(
	ctx context.Context,
	cfg exampleConfig,
	requireOSSandbox bool,
) error {
	cfg.Tools.CodeExecutor.Sandbox.OutputMaxBytes = 128
	rt, err := newScenarioRuntime(ctx, cfg, requireOSSandbox)
	if err != nil {
		return err
	}
	defer rt.close()
	text, err := rt.run(
		ctx,
		"output-cap",
		"Return exactly one Python fenced code block and no prose. "+
			"The code must print 10000 x characters.",
	)
	if err != nil {
		return err
	}
	return requireContainsAll(text, "[truncated]")
}

func runWorkspaceExecHidden(
	ctx context.Context,
	cfg exampleConfig,
	requireOSSandbox bool,
) error {
	rt, err := newScenarioRuntime(ctx, cfg, requireOSSandbox)
	if err != nil {
		return err
	}
	defer rt.close()
	names := make(map[string]bool)
	for _, name := range rt.runtime.ToolNames() {
		names[name] = true
	}
	for _, name := range []string{
		"workspace_exec",
		"workspace_write_stdin",
		"workspace_kill_session",
	} {
		if names[name] {
			return fmt.Errorf("unexpected workspace tool exposed: %s", name)
		}
	}
	return nil
}

func runDisabledProfileExplicit(
	_ context.Context,
	cfg exampleConfig,
	_ bool,
) error {
	if cfg.Tools.CodeExecutor.Sandbox.Profile == "disabled" {
		fmt.Println("disabled profile is explicitly selected in config")
		return nil
	}
	return nil
}

type scenarioRuntime struct {
	configPath       string
	runtime          *app.Runtime
	requireOSSandbox bool
}

func newScenarioRuntime(
	ctx context.Context,
	cfg exampleConfig,
	requireOSSandbox bool,
) (*scenarioRuntime, error) {
	if cfg.Tools.CodeExecutor.Type != "sandbox" {
		return nil, fmt.Errorf(
			"tools.code_executor.type must be sandbox, got %q",
			cfg.Tools.CodeExecutor.Type,
		)
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("OPENAI_API_KEY is not set; export OPENAI_API_KEY and optional OPENAI_BASE_URL/MODEL_NAME to run real model scenarios.")
		return nil, errSkip
	}
	configPath, err := writeScenarioConfig(cfg)
	if err != nil {
		return nil, err
	}
	rt, err := app.NewRuntime(ctx, []string{
		"-config", configPath,
		"-admin-enabled=false",
		"-agent-instruction",
		"You are validating OpenClaw sandbox code execution. " +
			"When asked to return code, return exactly one runnable " +
			"fenced code block and no prose.",
	})
	if err != nil {
		_ = os.Remove(configPath)
		if !requireOSSandbox && isSandboxSetupError(err) {
			fmt.Printf("managed sandbox unavailable: %v\n", err)
			return nil, errSkip
		}
		return nil, fmt.Errorf("create openclaw runtime failed: %w", err)
	}
	return &scenarioRuntime{
		configPath:       configPath,
		runtime:          rt,
		requireOSSandbox: requireOSSandbox,
	}, nil
}

func (rt *scenarioRuntime) close() {
	if rt == nil {
		return
	}
	if rt.runtime != nil {
		_ = rt.runtime.Close()
	}
	if rt.configPath != "" {
		_ = os.Remove(rt.configPath)
	}
}

func (rt *scenarioRuntime) run(
	ctx context.Context,
	sessionID string,
	prompt string,
) (string, error) {
	return rt.runInSession(ctx, sessionID, prompt)
}

func (rt *scenarioRuntime) runInSession(
	ctx context.Context,
	sessionID string,
	prompt string,
) (string, error) {
	events, err := rt.runtime.Run(
		ctx,
		"openclaw-sandbox-user",
		sessionID,
		model.NewUserMessage(prompt),
	)
	if err != nil {
		if !rt.requireOSSandbox && isSandboxSetupError(err) {
			fmt.Printf("managed sandbox unavailable: %v\n", err)
			return "", errSkip
		}
		return "", err
	}
	var out strings.Builder
	for event := range events {
		if event.Error != nil {
			if !rt.requireOSSandbox && isSandboxSetupError(event.Error) {
				fmt.Printf("managed sandbox unavailable: %v\n", event.Error)
				return "", errSkip
			}
			return out.String(), event.Error
		}
		for _, choice := range event.Response.Choices {
			if choice.Message.Content != "" {
				out.WriteString(choice.Message.Content)
				out.WriteByte('\n')
			}
			if choice.Delta.Content != "" {
				out.WriteString(choice.Delta.Content)
			}
		}
		if event.IsRunnerCompletion() {
			break
		}
	}
	return out.String(), nil
}

func writeScenarioConfig(cfg exampleConfig) (string, error) {
	if strings.TrimSpace(cfg.StateDir) == "" {
		cfg.StateDir = filepath.Join(os.TempDir(), appName)
	}
	if strings.TrimSpace(cfg.Model.Mode) == "" {
		cfg.Model.Mode = "openai"
	}
	if strings.TrimSpace(cfg.Model.Name) == "" {
		cfg.Model.Name = "glm-4.7-flash"
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal scenario config: %w", err)
	}
	file, err := os.CreateTemp("", "openclaw-sandbox-code-execution-*.yaml")
	if err != nil {
		return "", fmt.Errorf("create scenario config: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("write scenario config: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("close scenario config: %w", err)
	}
	return file.Name(), nil
}

func isSandboxSetupError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "sandbox") ||
		strings.Contains(text, "bubblewrap") ||
		strings.Contains(text, "bwrap") ||
		strings.Contains(text, "user namespace")
}

func requireContainsAll(text string, values ...string) error {
	for _, value := range values {
		if !strings.Contains(text, value) {
			return fmt.Errorf("missing %q in output: %s", value, trim(text))
		}
	}
	return nil
}

func trim(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 500 {
		return text[:500] + "..."
	}
	return text
}
