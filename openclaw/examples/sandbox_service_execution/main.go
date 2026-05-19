//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main validates OpenClaw sandbox execution through a real HTTP service.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
)

const (
	defaultScenario   = "basic-python"
	defaultConfigPath = "examples/sandbox_service_execution/openclaw.yaml"
	appName           = "openclaw-sandbox-service-execution"

	gatewayHealthPath = "/healthz"
	gatewayStreamPath = "/v1/gateway/messages:stream"

	startupTimeout  = 90 * time.Second
	requestTimeout  = 180 * time.Second
	shutdownTimeout = 5 * time.Second
	logLimitBytes   = 64 << 10
)

var errSkip = errors.New("skip")

type scenario struct {
	name string
	run  func(context.Context, exampleConfig, bool) error
}

type exampleConfig struct {
	AppName  string       `yaml:"app_name,omitempty"`
	StateDir string       `yaml:"state_dir,omitempty"`
	HTTP     httpConfig   `yaml:"http,omitempty"`
	Admin    adminConfig  `yaml:"admin,omitempty"`
	Model    modelConfig  `yaml:"model,omitempty"`
	Agent    agentConfig  `yaml:"agent,omitempty"`
	Tools    toolsConfig  `yaml:"tools,omitempty"`
	Session  storeConfig  `yaml:"session,omitempty"`
	Memory   memoryConfig `yaml:"memory,omitempty"`
}

type httpConfig struct {
	Addr string `yaml:"addr,omitempty"`
}

type adminConfig struct {
	Enabled *bool `yaml:"enabled,omitempty"`
}

type modelConfig struct {
	Mode          string `yaml:"mode,omitempty"`
	Name          string `yaml:"name,omitempty"`
	BaseURL       string `yaml:"base_url,omitempty"`
	OpenAIVariant string `yaml:"openai_variant,omitempty"`
}

type agentConfig struct {
	Instruction string `yaml:"instruction,omitempty"`
}

type toolsConfig struct {
	EnableOpenClawTools *bool              `yaml:"enable_openclaw_tools,omitempty"`
	EnableParallelTools *bool              `yaml:"enable_parallel_tools,omitempty"`
	CodeExecutor        codeExecutorConfig `yaml:"code_executor,omitempty"`
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
	OutputMaxBytes *int                  `yaml:"output_max_bytes,omitempty"`
	ShellEnv       sandboxShellEnvConfig `yaml:"shell_env,omitempty"`
}

type sandboxShellEnvConfig struct {
	Inherit              string   `yaml:"inherit,omitempty"`
	ApplyDefaultExcludes *bool    `yaml:"apply_default_excludes,omitempty"`
	Exclude              []string `yaml:"exclude,omitempty"`
	IncludeOnly          []string `yaml:"include_only,omitempty"`
}

type storeConfig struct {
	Backend string        `yaml:"backend,omitempty"`
	Summary summaryConfig `yaml:"summary,omitempty"`
}

type summaryConfig struct {
	Enabled *bool `yaml:"enabled,omitempty"`
}

type memoryConfig struct {
	Backend string     `yaml:"backend,omitempty"`
	Auto    autoConfig `yaml:"auto,omitempty"`
}

type autoConfig struct {
	Enabled *bool `yaml:"enabled,omitempty"`
}

func main() {
	configPath := flag.String(
		"config",
		defaultConfigPath,
		"OpenClaw YAML config used as the scenario base",
	)
	scenarioName := flag.String(
		"scenario",
		defaultScenario,
		"scenario name or all",
	)
	requireOSSandbox := flag.Bool(
		"require-os-sandbox",
		true,
		"fail when managed OS sandbox setup is unavailable",
	)
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if err := runScenarios(context.Background(), cfg, *scenarioName, *requireOSSandbox); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox OpenClaw service example failed: %v\n", err)
		os.Exit(1)
	}
}

func loadConfig(path string) (exampleConfig, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return exampleConfig{}, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return exampleConfig{}, err
	}
	normalizeConfig(&cfg)
	return cfg, nil
}

func defaultConfig() exampleConfig {
	autoExecute := true
	applyDefaultExcludes := true
	disabled := false
	outputMax := 1 << 20
	return exampleConfig{
		AppName:  appName,
		StateDir: filepath.Join(os.TempDir(), appName),
		HTTP: httpConfig{
			Addr: ":8080",
		},
		Admin: adminConfig{Enabled: &disabled},
		Model: modelConfig{
			Mode:          "openai",
			Name:          "glm-4.7-flash",
			OpenAIVariant: "auto",
		},
		Agent: agentConfig{Instruction: serviceInstruction()},
		Tools: toolsConfig{
			EnableOpenClawTools: &disabled,
			EnableParallelTools: &disabled,
			CodeExecutor: codeExecutorConfig{
				Type:                  "sandbox",
				AutoExecuteCodeBlocks: &autoExecute,
				Sandbox: sandboxExecutorConfig{
					Backend:        "auto",
					Profile:        "workspace_write",
					Network:        "restricted",
					DefaultTimeout: "30s",
					OutputMaxBytes: &outputMax,
					ShellEnv: sandboxShellEnvConfig{
						Inherit:              "core",
						ApplyDefaultExcludes: &applyDefaultExcludes,
					},
				},
			},
		},
		Session: storeConfig{
			Backend: "inmemory",
			Summary: summaryConfig{Enabled: &disabled},
		},
		Memory: memoryConfig{
			Backend: "inmemory",
			Auto:    autoConfig{Enabled: &disabled},
		},
	}
}

func normalizeConfig(cfg *exampleConfig) {
	defaults := defaultConfig()
	if cfg.AppName == "" {
		cfg.AppName = defaults.AppName
	}
	if cfg.HTTP.Addr == "" {
		cfg.HTTP.Addr = defaults.HTTP.Addr
	}
	if cfg.Admin.Enabled == nil {
		cfg.Admin.Enabled = defaults.Admin.Enabled
	}
	if cfg.Model.Mode == "" {
		cfg.Model.Mode = defaults.Model.Mode
	}
	if cfg.Model.Name == "" {
		cfg.Model.Name = defaults.Model.Name
	}
	if cfg.Model.OpenAIVariant == "" {
		cfg.Model.OpenAIVariant = defaults.Model.OpenAIVariant
	}
	if cfg.Agent.Instruction == "" {
		cfg.Agent.Instruction = defaults.Agent.Instruction
	}
	if cfg.Tools.EnableOpenClawTools == nil {
		cfg.Tools.EnableOpenClawTools = defaults.Tools.EnableOpenClawTools
	}
	if cfg.Tools.EnableParallelTools == nil {
		cfg.Tools.EnableParallelTools = defaults.Tools.EnableParallelTools
	}
	if cfg.Tools.CodeExecutor.Type == "" {
		cfg.Tools.CodeExecutor.Type = defaults.Tools.CodeExecutor.Type
	}
	if cfg.Tools.CodeExecutor.AutoExecuteCodeBlocks == nil {
		cfg.Tools.CodeExecutor.AutoExecuteCodeBlocks = defaults.Tools.CodeExecutor.AutoExecuteCodeBlocks
	}
	sandbox := &cfg.Tools.CodeExecutor.Sandbox
	defaultSandbox := defaults.Tools.CodeExecutor.Sandbox
	if sandbox.Backend == "" {
		sandbox.Backend = defaultSandbox.Backend
	}
	if sandbox.Profile == "" {
		sandbox.Profile = defaultSandbox.Profile
	}
	if sandbox.Network == "" {
		sandbox.Network = defaultSandbox.Network
	}
	if sandbox.DefaultTimeout == "" {
		sandbox.DefaultTimeout = defaultSandbox.DefaultTimeout
	}
	if sandbox.OutputMaxBytes == nil {
		sandbox.OutputMaxBytes = defaultSandbox.OutputMaxBytes
	}
	if sandbox.ShellEnv.Inherit == "" {
		sandbox.ShellEnv.Inherit = defaultSandbox.ShellEnv.Inherit
	}
	if sandbox.ShellEnv.ApplyDefaultExcludes == nil {
		sandbox.ShellEnv.ApplyDefaultExcludes = defaultSandbox.ShellEnv.ApplyDefaultExcludes
	}
	if cfg.Session.Backend == "" {
		cfg.Session.Backend = defaults.Session.Backend
	}
	if cfg.Session.Summary.Enabled == nil {
		cfg.Session.Summary.Enabled = defaults.Session.Summary.Enabled
	}
	if cfg.Memory.Backend == "" {
		cfg.Memory.Backend = defaults.Memory.Backend
	}
	if cfg.Memory.Auto.Enabled == nil {
		cfg.Memory.Auto.Enabled = defaults.Memory.Auto.Enabled
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

func runBasicPython(ctx context.Context, cfg exampleConfig, requireOSSandbox bool) error {
	svc, err := startScenarioService(ctx, cfg, "basic-python", requireOSSandbox)
	if err != nil {
		return err
	}
	defer svc.close()
	text, err := svc.runPrompt(
		ctx,
		"basic-python",
		codePrompt(
			"compute count, sum, and mean for [5, 12, 8, 15, 7, 9, 11] "+
				"and print compact JSON with keys count, sum, mean.",
		),
	)
	if err != nil {
		return err
	}
	return requireContainsAll(text, `"count"`, "7", `"sum"`, "67", `"mean"`)
}

func runSessionPersistence(ctx context.Context, cfg exampleConfig, requireOSSandbox bool) error {
	svc, err := startScenarioService(ctx, cfg, "session-persistence", requireOSSandbox)
	if err != nil {
		return err
	}
	defer svc.close()

	sessionID := "session-persistence"
	_, err = svc.runPrompt(
		ctx,
		sessionID,
		codePrompt(
			"write the text openclaw-sandbox-ok to persist.txt in the "+
				"current working directory and print WROTE.",
		),
	)
	if err != nil {
		return err
	}
	text, err := svc.runPrompt(
		ctx,
		sessionID,
		codePrompt(
			"read persist.txt from the current working directory and print "+
				"its contents.",
		),
	)
	if err != nil {
		return err
	}
	return requireContainsAll(text, "openclaw-sandbox-ok")
}

func runEnvRedaction(ctx context.Context, cfg exampleConfig, requireOSSandbox bool) error {
	svc, err := startScenarioService(ctx, cfg, "env-redaction", requireOSSandbox)
	if err != nil {
		return err
	}
	defer svc.close()
	text, err := svc.runPrompt(
		ctx,
		"env-redaction",
		codePrompt(
			"print JSON booleans for whether OPENAI_API_KEY, OPENAI_BASE_URL, "+
				"and GLM_API_KEY are present in os.environ. Do not print any "+
				"environment variable values.",
		),
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
	return requireContainsAll(text, "OPENAI_API_KEY", "OPENAI_BASE_URL", "GLM_API_KEY")
}

func runNetworkRestricted(ctx context.Context, cfg exampleConfig, requireOSSandbox bool) error {
	svc, err := startScenarioService(ctx, cfg, "network-restricted", requireOSSandbox)
	if err != nil {
		return err
	}
	defer svc.close()
	text, err := svc.runPrompt(
		ctx,
		"network-restricted",
		codePrompt(
			"try to open https://example.com with a 2 second timeout. Print "+
				"network_unexpected_success if it succeeds; otherwise print "+
				"network_blocked and the exception type.",
		),
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

func runTimeout(ctx context.Context, cfg exampleConfig, requireOSSandbox bool) error {
	cfg.Tools.CodeExecutor.Sandbox.DefaultTimeout = "1s"
	svc, err := startScenarioService(ctx, cfg, "timeout", requireOSSandbox)
	if err != nil {
		return err
	}
	defer svc.close()
	text, err := svc.runPrompt(
		ctx,
		"timeout",
		codePrompt("sleep for 5 seconds, then print too_late."),
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

func runOutputCap(ctx context.Context, cfg exampleConfig, requireOSSandbox bool) error {
	cfg.Tools.CodeExecutor.Sandbox.OutputMaxBytes = intPtr(128)
	svc, err := startScenarioService(ctx, cfg, "output-cap", requireOSSandbox)
	if err != nil {
		return err
	}
	defer svc.close()
	text, err := svc.runPrompt(
		ctx,
		"output-cap",
		codePrompt("print 10000 x characters."),
	)
	if err != nil {
		return err
	}
	return requireContainsAll(text, "[truncated]")
}

type openClawService struct {
	baseURL  string
	stateDir string
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	done     <-chan error
	logs     *processLog
}

func startScenarioService(
	ctx context.Context,
	cfg exampleConfig,
	name string,
	requireOSSandbox bool,
) (*openClawService, error) {
	if err := checkPreflight(requireOSSandbox); err != nil {
		return nil, err
	}
	if strings.ToLower(strings.TrimSpace(cfg.Tools.CodeExecutor.Type)) != "sandbox" {
		return nil, fmt.Errorf(
			"tools.code_executor.type must be sandbox, got %q",
			cfg.Tools.CodeExecutor.Type,
		)
	}

	openclawDir, err := resolveOpenClawDir()
	if err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp("", appName+"-"+name+"-")
	if err != nil {
		return nil, err
	}
	cfg.StateDir = filepath.Join(tempDir, "state")
	cfg.HTTP.Addr = ":0"
	configPath, err := writeScenarioConfig(tempDir, cfg)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}
	addr, err := freeLoopbackAddr()
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}

	childCtx, cancel := context.WithCancel(ctx)
	logs := &processLog{max: logLimitBytes}
	args := []string{
		"run", "./cmd/openclaw",
		"-config", configPath,
		"-state-dir", cfg.StateDir,
		"-http-addr", addr,
		"-admin-enabled=false",
		"-agent-instruction", serviceInstruction(),
		"-debug-recorder",
		"-debug-recorder-mode", "safe",
		"-debug-recorder-dir", filepath.Join(cfg.StateDir, "debug"),
	}
	cmd := exec.CommandContext(childCtx, "go", args...)
	cmd.Dir = openclawDir
	cmd.Env = os.Environ()
	cmd.Stdout = logs
	cmd.Stderr = logs
	configureProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("start openclaw service: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	svc := &openClawService{
		baseURL:  "http://" + addr,
		stateDir: cfg.StateDir,
		cmd:      cmd,
		cancel:   cancel,
		done:     done,
		logs:     logs,
	}
	if err := svc.waitForReady(ctx); err != nil {
		svc.close()
		_ = os.RemoveAll(tempDir)
		return nil, err
	}
	fmt.Printf("service: %s state: %s\n", svc.baseURL, cfg.StateDir)
	return svc, nil
}

func (s *openClawService) waitForReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf(
				"openclaw service did not become ready: %w\n%s",
				ctx.Err(),
				s.logs.String(),
			)
		case err := <-s.done:
			return fmt.Errorf("openclaw service exited early: %w\n%s", err, s.logs.String())
		case <-ticker.C:
			req, err := http.NewRequestWithContext(
				ctx,
				http.MethodGet,
				s.baseURL+gatewayHealthPath,
				nil,
			)
			if err != nil {
				return err
			}
			rsp, err := client.Do(req)
			if err != nil {
				continue
			}
			_, _ = io.Copy(io.Discard, rsp.Body)
			_ = rsp.Body.Close()
			if rsp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

func (s *openClawService) runPrompt(
	ctx context.Context,
	sessionID string,
	prompt string,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	requestID := sessionID + "-" + time.Now().Format("20060102150405.000000000")

	body, err := json.Marshal(streamMessageRequest{
		MessageRequest: gwproto.MessageRequest{
			Channel:   "sandbox-service",
			From:      "openclaw-sandbox-user",
			UserID:    "openclaw-sandbox-user",
			SessionID: sessionID,
			RequestID: requestID,
			Text:      prompt,
		},
		StreamOptions: &gwproto.MessageStreamOptions{
			ProgressAfterTextDelta: true,
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		s.baseURL+gatewayStreamPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gateway request failed: %w\n%s", err, s.logs.String())
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(rsp.Body, 8<<10))
		return "", fmt.Errorf(
			"gateway status %d: %s\n%s",
			rsp.StatusCode,
			strings.TrimSpace(string(data)),
			s.logs.String(),
		)
	}
	text, err := readStreamText(rsp.Body)
	if err != nil {
		return text, err
	}
	if strings.TrimSpace(text) != "" {
		fmt.Printf("gateway reply: %s\n", trim(text))
	}
	result, err := s.waitForCodeExecutionResult(ctx, requestID)
	if err != nil {
		return "", fmt.Errorf("%w\ngateway reply: %s\n%s", err, trim(text), s.logs.String())
	}
	fmt.Printf("code execution result: %s\n", trim(result))
	return result, nil
}

func (s *openClawService) close() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	select {
	case <-s.done:
	case <-timer.C:
		if s.cmd != nil && s.cmd.Process != nil {
			killProcessGroup(s.cmd)
			select {
			case <-s.done:
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (s *openClawService) waitForCodeExecutionResult(
	ctx context.Context,
	requestID string,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	debugRoot := filepath.Join(s.stateDir, "debug")
	var lastErr error
	for {
		traceDir, err := traceDirForRequest(debugRoot, requestID)
		if err == nil {
			result, readErr := readCodeExecutionResult(traceDir)
			if readErr == nil {
				return result, nil
			}
			lastErr = readErr
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf(
				"code execution result not found for request %s: %v",
				requestID,
				lastErr,
			)
		case <-ticker.C:
		}
	}
}

func traceDirForRequest(debugRoot string, requestID string) (string, error) {
	var found string
	errFound := errors.New("trace found")
	err := filepath.WalkDir(debugRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || d.Name() != "meta.json" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var meta struct {
			Start struct {
				RequestID string `json:"request_id,omitempty"`
			} `json:"start,omitempty"`
		}
		if json.Unmarshal(data, &meta) != nil {
			return nil
		}
		if meta.Start.RequestID != requestID {
			return nil
		}
		found = filepath.Dir(path)
		return errFound
	})
	if errors.Is(err, errFound) {
		return found, nil
	}
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errors.New("trace metadata not found")
	}
	return found, nil
}

func readCodeExecutionResult(traceDir string) (string, error) {
	data, err := debugrecorder.ReadEventsFile(traceDir)
	if err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for scanner.Scan() {
		var rec struct {
			Kind    string          `json:"kind,omitempty"`
			Payload json.RawMessage `json:"payload,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			return "", err
		}
		if rec.Kind != debugrecorder.KindRunnerEvent {
			continue
		}
		var evt struct {
			Object  string `json:"object,omitempty"`
			Tag     string `json:"tag,omitempty"`
			Choices []struct {
				Message struct {
					Content string `json:"content,omitempty"`
				} `json:"message,omitempty"`
			} `json:"choices,omitempty"`
		}
		if err := json.Unmarshal(rec.Payload, &evt); err != nil {
			return "", err
		}
		if evt.Object != model.ObjectTypePostprocessingCodeExecution ||
			!strings.Contains(evt.Tag, event.CodeExecutionResultTag) {
			continue
		}
		if len(evt.Choices) == 0 {
			return "", errors.New("code execution result has no choices")
		}
		content := strings.TrimSpace(evt.Choices[0].Message.Content)
		if content == "" {
			return "", errors.New("code execution result is empty")
		}
		return content, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("code execution result event not found")
}

type streamMessageRequest struct {
	gwproto.MessageRequest
	StreamOptions *gwproto.MessageStreamOptions `json:"stream_options,omitempty"`
}

func readStreamText(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	var out strings.Builder
	var data strings.Builder
	flush := func() error {
		raw := strings.TrimSpace(data.String())
		data.Reset()
		if raw == "" {
			return nil
		}
		var evt gwproto.StreamEvent
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			return fmt.Errorf("decode stream event: %w: %s", err, trim(raw))
		}
		switch evt.Type {
		case gwproto.StreamEventTypeMessageDelta,
			gwproto.StreamEventTypePublicDelta:
			out.WriteString(evt.Delta)
		case gwproto.StreamEventTypeMessageCompleted,
			gwproto.StreamEventTypePublicCompleted:
			if evt.Reply != "" {
				if out.Len() > 0 {
					out.WriteByte('\n')
				}
				out.WriteString(evt.Reply)
			}
		case gwproto.StreamEventTypeRunError:
			if evt.Error == nil {
				return errors.New("gateway stream run error")
			}
			return fmt.Errorf("%s: %s", evt.Error.Type, evt.Error.Message)
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return out.String(), err
			}
		case strings.HasPrefix(line, gwproto.SSEDataLinePrefix):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(line, gwproto.SSEDataLinePrefix))
		case strings.HasPrefix(line, gwproto.SSEDataPrefix):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, gwproto.SSEDataPrefix)))
		}
	}
	if err := scanner.Err(); err != nil {
		return out.String(), err
	}
	if err := flush(); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

func writeScenarioConfig(dir string, cfg exampleConfig) (string, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal scenario config: %w", err)
	}
	path := filepath.Join(dir, "openclaw.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write scenario config: %w", err)
	}
	return path, nil
}

func checkPreflight(requireOSSandbox bool) error {
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("OPENAI_API_KEY is not set; source ./glm.sh to run real model scenarios.")
		return errSkip
	}
	if runtime.GOOS != "linux" {
		return sandboxUnavailable(requireOSSandbox, "managed sandbox requires linux")
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return sandboxUnavailable(requireOSSandbox, "bubblewrap executable not found in PATH")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		return fmt.Errorf("python3 executable not found in PATH: %w", err)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		return fmt.Errorf("bash executable not found in PATH: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe := exec.CommandContext(
		ctx,
		bwrap,
		"--die-with-parent",
		"--unshare-user",
		"--ro-bind", "/", "/",
		"--proc", "/proc",
		"--", "/bin/true",
	)
	if err := probe.Run(); err != nil {
		return sandboxUnavailable(
			requireOSSandbox,
			fmt.Sprintf("bubblewrap preflight failed: %v", err),
		)
	}
	return nil
}

func sandboxUnavailable(require bool, msg string) error {
	if !require {
		fmt.Printf("managed sandbox unavailable: %s\n", msg)
		return errSkip
	}
	return errors.New(msg)
}

func freeLoopbackAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

func resolveOpenClawDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if fileExists(filepath.Join(wd, "cmd", "openclaw", "main.go")) {
		return wd, nil
	}
	candidate := filepath.Join(wd, "openclaw")
	if fileExists(filepath.Join(candidate, "cmd", "openclaw", "main.go")) {
		return candidate, nil
	}
	return "", fmt.Errorf("cannot locate openclaw module from %s", wd)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func codePrompt(task string) string {
	return "This is a deployed OpenClaw sandbox validation. " +
		"On your first assistant response, return exactly one Python fenced " +
		"code block and no prose. The code must " + task + " " +
		"Do not solve the task directly. After OpenClaw provides the code " +
		"execution result, reply only with the relevant program output or " +
		"error marker, without markdown."
}

func serviceInstruction() string {
	return "You are validating OpenClaw sandbox code execution through the " +
		"HTTP gateway. When asked to produce code, return exactly one " +
		"runnable fenced code block and no prose. After code execution " +
		"results are provided, answer only with the relevant program output."
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
	if len(text) > 600 {
		return text[:600] + "..."
	}
	return text
}

func intPtr(v int) *int { return &v }

type processLog struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (l *processLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.max <= 0 || len(l.buf) < l.max {
		remaining := l.max - len(l.buf)
		if l.max <= 0 || len(p) <= remaining {
			l.buf = append(l.buf, p...)
		} else {
			l.buf = append(l.buf, p[:remaining]...)
		}
	}
	return len(p), nil
}

func (l *processLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buf) == 0 {
		return "(no service output captured)"
	}
	out := string(l.buf)
	if l.max > 0 && len(l.buf) >= l.max {
		out += "\n[service log truncated]\n"
	}
	return out
}
