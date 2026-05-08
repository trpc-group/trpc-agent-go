//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package e2b provides a CodeExecutor implementation for E2B.
package e2b

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	ci "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/codeinterpreter"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Option configures a CodeExecutor.
type Option func(*CodeExecutor)

// WithAPIKey sets the E2B API key. When empty the E2B_API_KEY env var is used.
func WithAPIKey(apiKey string) Option {
	return func(c *CodeExecutor) { c.apiKey = apiKey }
}

// WithAccessToken sets the envd access token.
func WithAccessToken(token string) Option {
	return func(c *CodeExecutor) { c.accessToken = token }
}

// WithDomain overrides the E2B domain (default: e2b.app).
func WithDomain(domain string) Option {
	return func(c *CodeExecutor) { c.domain = domain }
}

// WithDebug toggles debug mode (plain HTTP to local sandboxes).
func WithDebug(debug bool) Option {
	return func(c *CodeExecutor) { c.debug = debug }
}

// WithTemplate sets the sandbox template (default: code-interpreter-v1).
func WithTemplate(template string) Option {
	return func(c *CodeExecutor) { c.template = template }
}

// WithSandboxTimeout sets the wall-clock lifetime of the sandbox.
func WithSandboxTimeout(t time.Duration) Option {
	return func(c *CodeExecutor) { c.sandboxTimeout = t }
}

// WithRequestTimeout sets the HTTP request timeout.
func WithRequestTimeout(t time.Duration) Option {
	return func(c *CodeExecutor) { c.requestTimeout = t }
}

// WithExecutionTimeout sets the per-cell code execution timeout.
// Use a negative value to disable timeouts.
func WithExecutionTimeout(t time.Duration) Option {
	return func(c *CodeExecutor) { c.executionTimeout = t }
}

// WithEnvVars sets environment variables injected into the sandbox at start.
func WithEnvVars(vars map[string]string) Option {
	return func(c *CodeExecutor) { c.envVars = vars }
}

// WithMetadata attaches metadata to the sandbox.
func WithMetadata(meta map[string]string) Option {
	return func(c *CodeExecutor) { c.metadata = meta }
}

// WithHTTPClient overrides the underlying HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *CodeExecutor) { c.httpClient = h }
}

// WithHeaders sets additional HTTP headers applied to every API call.
func WithHeaders(headers map[string]string) Option {
	return func(c *CodeExecutor) { c.headers = headers }
}

// WithSandboxID connects to an existing sandbox instead of creating one.
func WithSandboxID(sandboxID string) Option {
	return func(c *CodeExecutor) { c.sandboxID = sandboxID }
}

// WithLanguage sets the default language used when a code block does not
// specify one (default: python).
func WithLanguage(lang ci.RunCodeLanguage) Option {
	return func(c *CodeExecutor) { c.defaultLanguage = lang }
}

// WithSandboxRunBase sets the base directory **inside the sandbox** where
// per-execution workspaces are created (default: /tmp/run).
func WithSandboxRunBase(dir string) Option {
	return func(c *CodeExecutor) { c.sandboxRunBase = dir }
}

// CodeExecutor executes code inside an E2B code-interpreter sandbox.
type CodeExecutor struct {
	mu sync.Mutex

	// Connection-level options.
	apiKey         string
	accessToken    string
	domain         string
	debug          bool
	template       string
	sandboxTimeout time.Duration
	requestTimeout time.Duration
	envVars        map[string]string
	metadata       map[string]string
	httpClient     *http.Client
	headers        map[string]string
	sandboxID      string

	// Execution-level options.
	executionTimeout time.Duration
	defaultLanguage  ci.RunCodeLanguage

	// Workspace integration (runs entirely inside the sandbox).
	sandboxRunBase string
	rt             *workspaceRuntime

	// Sandbox instance.
	sbx *ci.Sandbox
	// owned indicates whether the CodeExecutor owns the sandbox lifecycle
	// (i.e., it created the sandbox itself and should kill it on Close).
	owned bool
}

// New creates a new CodeExecutor. When `WithSandboxID` is supplied it connects
// to an existing sandbox; otherwise a new sandbox is created.
func New(opts ...Option) (*CodeExecutor, error) {
	return NewWithContext(context.Background(), opts...)
}

// NewWithContext is like New but accepts a context used for sandbox setup.
func NewWithContext(ctx context.Context, opts ...Option) (*CodeExecutor, error) {
	c := &CodeExecutor{
		template:         ci.DefaultTemplate,
		sandboxTimeout:   ci.DefaultSandboxTimeout * time.Second,
		requestTimeout:   ci.DefaultRequestTimeout * time.Second,
		executionTimeout: ci.DefaultTimeout * time.Second,
		defaultLanguage:  ci.LanguagePython,
	}
	for _, opt := range opts {
		opt(c)
	}

	sbxOpts := &ci.SandboxOpts{
		APIKey:         c.apiKey,
		AccessToken:    c.accessToken,
		Domain:         c.domain,
		Debug:          c.debug,
		RequestTimeout: c.requestTimeout,
		Timeout:        c.sandboxTimeout,
		Template:       c.template,
		Metadata:       c.metadata,
		EnvVars:        c.envVars,
		HTTPClient:     c.httpClient,
		Headers:        c.headers,
	}

	var (
		sbx *ci.Sandbox
		err error
	)
	if c.sandboxID != "" {
		sbx, err = ci.Connect(ctx, c.sandboxID, sbxOpts)
		c.owned = false
	} else {
		sbx, err = ci.Create(ctx, sbxOpts)
		c.owned = true
	}
	if err != nil {
		return nil, fmt.Errorf("e2b: create/connect sandbox: %w", err)
	}
	c.sbx = sbx

	// Workspace runtime runs all file/program operations inside the sandbox.
	c.rt = newWorkspaceRuntime(c)

	log.Debugf("e2b sandbox ready: id=%s", sbx.SandboxID())
	return c, nil
}

// SandboxID returns the current sandbox id.
func (c *CodeExecutor) SandboxID() string {
	if c.sbx == nil {
		return ""
	}
	return c.sbx.SandboxID()
}

// Sandbox exposes the underlying sandbox for advanced usage.
func (c *CodeExecutor) Sandbox() *ci.Sandbox { return c.sbx }

// CodeBlockDelimiter returns the fenced code delimiter.
func (c *CodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

// ExecuteCode executes all code blocks sequentially in the sandbox and
// aggregates their output.
func (c *CodeExecutor) ExecuteCode(
	ctx context.Context, input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	if c.sbx == nil {
		return codeexecutor.CodeExecutionResult{}, fmt.Errorf(
			"e2b: sandbox not initialized",
		)
	}

	var (
		out       strings.Builder
		outFiles  []codeexecutor.File
		fileIndex int
	)

	for i, block := range input.CodeBlocks {
		lang := pickLanguage(block.Language, c.defaultLanguage)
		exec, err := c.sbx.RunCode(ctx, block.Code, &ci.RunCodeOpts{
			Language: lang,
			Timeout:  c.executionTimeout,
			OnStdout: func(m ci.OutputMessage) {
				out.WriteString(m.Line)
			},
			OnStderr: func(m ci.OutputMessage) {
				appendStderr(&out, m.Line)
			},
		})
		if err != nil {
			return codeexecutor.CodeExecutionResult{}, fmt.Errorf(
				"e2b: execute block %d: %w", i, err,
			)
		}

		for _, r := range exec.Results {
			files, text := extractFromResult(r, i, &fileIndex)
			if text != "" {
				if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
					out.WriteString("\n")
				}
				out.WriteString(text)
			}
			outFiles = append(outFiles, files...)
		}

		if exec.Error != nil {
			// Surface the execution error in the aggregated output.
			if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
				out.WriteString("\n")
			}
			out.WriteString(fmt.Sprintf("[error] %s: %s\n",
				exec.Error.Name, exec.Error.Value))
			if exec.Error.Traceback != "" {
				out.WriteString(exec.Error.Traceback)
				if !strings.HasSuffix(exec.Error.Traceback, "\n") {
					out.WriteString("\n")
				}
			}
		}
	}

	return codeexecutor.CodeExecutionResult{
		Output:      out.String(),
		OutputFiles: outFiles,
	}, nil
}

// pickLanguage maps a code-block language string to an E2B language
// identifier, falling back to the configured default.
func pickLanguage(
	lang string, def ci.RunCodeLanguage,
) ci.RunCodeLanguage {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "auto":
		return def
	case "python", "py", "python3":
		return ci.LanguagePython
	case "javascript", "js", "node", "nodejs":
		return ci.LanguageJavaScript
	case "typescript", "ts":
		return ci.LanguageTypeScript
	case "bash", "sh", "shell":
		return ci.LanguageBash
	case "r":
		return ci.LanguageR
	case "java":
		return ci.LanguageJava
	default:
		// Pass through unknown languages; the sandbox may support them as
		// user-installed kernels.
		return ci.RunCodeLanguage(lang)
	}
}

// appendStderr writes a stderr chunk to the output buffer, prefixing each line
// so users can distinguish stderr from stdout.
func appendStderr(out *strings.Builder, line string) {
	if line == "" {
		return
	}
	// Preserve trailing newlines while still prefixing every non-empty line.
	trimmed := strings.TrimRight(line, "\n")
	nlSuffix := line[len(trimmed):]
	for i, seg := range strings.Split(trimmed, "\n") {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString("[stderr] ")
		out.WriteString(seg)
	}
	out.WriteString(nlSuffix)
}

// extractFromResult turns a *ci.Result into text to be appended to the
// aggregated output and any binary representations into output files.
func extractFromResult(
	r *ci.Result, blockIdx int, fileIdx *int,
) ([]codeexecutor.File, string) {
	if r == nil {
		return nil, ""
	}
	var (
		files []codeexecutor.File
		text  strings.Builder
	)

	if r.Text != "" {
		text.WriteString(r.Text)
	}
	if r.HTML != "" {
		if text.Len() > 0 {
			text.WriteString("\n")
		}
		text.WriteString(r.HTML)
	}
	if r.Markdown != "" {
		if text.Len() > 0 {
			text.WriteString("\n")
		}
		text.WriteString(r.Markdown)
	}

	type binRep struct {
		ext, mime, payload string
		base64             bool
	}
	reps := []binRep{
		{".png", "image/png", r.PNG, true},
		{".jpeg", "image/jpeg", r.JPEG, true},
		{".pdf", "application/pdf", r.PDF, true},
		{".svg", "image/svg+xml", r.SVG, false},
		{".tex", "application/x-tex", r.LaTeX, false},
	}
	for _, rep := range reps {
		if rep.payload == "" {
			continue
		}
		name := fmt.Sprintf(
			"result_%d_%d%s", blockIdx, *fileIdx, rep.ext,
		)
		*fileIdx++
		f := codeexecutor.File{
			Name:     name,
			MIMEType: rep.mime,
		}
		if rep.base64 {
			// Decode base64 payload to raw bytes; fall back to raw payload
			// if decoding fails (the server may sometimes return raw data).
			if data, err := base64.StdEncoding.DecodeString(rep.payload); err == nil {
				f.Content = string(data)
				f.SizeBytes = int64(len(data))
			} else {
				f.Content = rep.payload
				f.SizeBytes = int64(len(rep.payload))
			}
		} else {
			f.Content = rep.payload
			f.SizeBytes = int64(len(rep.payload))
		}
		files = append(files, f)
	}

	return files, text.String()
}

// ensureRuntime returns the sandbox workspace runtime, lazily creating it
// for CodeExecutor instances that are used before a sandbox is attached
func (c *CodeExecutor) ensureRuntime() *workspaceRuntime {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rt == nil {
		c.rt = newWorkspaceRuntime(c)
	}
	return c.rt
}

// CreateWorkspace creates a workspace inside the sandbox.
func (c *CodeExecutor) CreateWorkspace(
	ctx context.Context, execID string, pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return c.ensureRuntime().CreateWorkspace(ctx, execID, pol)
}

// Cleanup removes the workspace directory inside the sandbox.
func (c *CodeExecutor) Cleanup(
	ctx context.Context, ws codeexecutor.Workspace,
) error {
	return c.ensureRuntime().Cleanup(ctx, ws)
}

// PutFiles writes files into the sandbox workspace.
func (c *CodeExecutor) PutFiles(
	ctx context.Context, ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	return c.ensureRuntime().PutFiles(ctx, ws, files)
}

// PutDirectory copies a host directory into the sandbox workspace.
func (c *CodeExecutor) PutDirectory(
	ctx context.Context, ws codeexecutor.Workspace, hostPath, to string,
) error {
	return c.ensureRuntime().PutDirectory(ctx, ws, hostPath, to)
}

// StageDirectory stages a host directory with options into the sandbox.
func (c *CodeExecutor) StageDirectory(
	ctx context.Context, ws codeexecutor.Workspace,
	src, to string, opt codeexecutor.StageOptions,
) error {
	return c.ensureRuntime().StageDirectory(ctx, ws, src, to, opt)
}

// RunProgram executes a command inside the sandbox workspace.
func (c *CodeExecutor) RunProgram(
	ctx context.Context, ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	return c.ensureRuntime().RunProgram(ctx, ws, spec)
}

// Collect reads matching files from the sandbox workspace.
func (c *CodeExecutor) Collect(
	ctx context.Context, ws codeexecutor.Workspace, patterns []string,
) ([]codeexecutor.File, error) {
	return c.ensureRuntime().Collect(ctx, ws, patterns)
}

// StageInputs maps external inputs into the sandbox workspace.
func (c *CodeExecutor) StageInputs(
	ctx context.Context, ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	return c.ensureRuntime().StageInputs(ctx, ws, specs)
}

// CollectOutputs applies the declarative output spec in the sandbox.
func (c *CodeExecutor) CollectOutputs(
	ctx context.Context, ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return c.ensureRuntime().CollectOutputs(ctx, ws, spec)
}

// ExecuteInline writes inline code blocks into the sandbox and runs them.
func (c *CodeExecutor) ExecuteInline(
	ctx context.Context, execID string,
	blocks []codeexecutor.CodeBlock, timeout time.Duration,
) (codeexecutor.RunResult, error) {
	return c.ensureRuntime().ExecuteInline(ctx, execID, blocks, timeout)
}

// Engine exposes the sandbox-backed runtime as an Engine for skill tools.
func (c *CodeExecutor) Engine() codeexecutor.Engine {
	rt := c.ensureRuntime()
	return codeexecutor.NewEngine(rt, rt, rt)
}

// Close terminates the owned sandbox (if any).
func (c *CodeExecutor) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sbx != nil && c.owned {
		if err := c.sbx.Kill(context.Background()); err != nil {
			log.Debugf("e2b: kill sandbox: %v", err)
			return err
		}
	}
	c.sbx = nil
	return nil
}
