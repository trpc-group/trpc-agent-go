//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package opensandbox provides a CodeExecutor implementation for
// OpenSandbox, an open-source sandbox platform (Alibaba) with strong
// isolation (gVisor / Kata / Firecracker microVM) and Kubernetes
// elastic scheduling.
package opensandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Option configures a CodeExecutor.
type Option func(*CodeExecutor)

// WithAPIKey sets the OpenSandbox API key.
func WithAPIKey(apiKey string) Option {
	return func(c *CodeExecutor) { c.apiKey = apiKey }
}

// WithDomain sets the OpenSandbox server domain (e.g. "localhost:8080").
func WithDomain(domain string) Option {
	return func(c *CodeExecutor) { c.domain = domain }
}

// WithProtocol sets the protocol ("http" or "https"). Defaults to
// "http" (DefaultProtocol) when empty.
func WithProtocol(protocol string) Option {
	return func(c *CodeExecutor) { c.protocol = protocol }
}

// WithImage sets the sandbox container image URI. When empty the SDK
// default CodeInterpreterImage is used.
func WithImage(image string) Option {
	return func(c *CodeExecutor) { c.image = image }
}

// WithEntrypoint overrides the sandbox entrypoint. When empty the SDK
// default CodeInterpreterEntrypoint is used.
func WithEntrypoint(entrypoint []string) Option {
	return func(c *CodeExecutor) { c.entrypoint = entrypoint }
}

// WithResourceLimits sets CPU/memory/GPU limits
// (e.g. {"cpu": "500m", "memory": "256Mi"}).
func WithResourceLimits(limits osb.ResourceLimits) Option {
	return func(c *CodeExecutor) { c.resourceLimits = limits }
}

// WithSandboxTimeout sets the wall-clock lifetime of the sandbox.
func WithSandboxTimeout(t time.Duration) Option {
	return func(c *CodeExecutor) { c.sandboxTimeout = t }
}

// WithRequestTimeout sets the HTTP request timeout for the OpenSandbox
// server. The SDK applies this timeout to the underlying HTTP client,
// which is shared by all requests — including the streaming /command
// endpoint used by RunProgram. To prevent the HTTP client from killing
// a long-running streaming /command call before the per-command
// execution timeout fires, NewWithContext silently raises requestTimeout
// to at least executionTimeout + requestTimeoutBuffer when the user-
// supplied value is smaller. If a caller passes RunProgramSpec.Timeout
// greater than requestTimeout - requestTimeoutBuffer, RunProgram
// returns an error instead of silently shortening the timeout; raise
// this option (or WithExecutionTimeout) to allow longer individual
// runs. Set t to 0 to use the SDK default (osb.DefaultRequestTimeout),
// which is then clamped like any other value.
func WithRequestTimeout(t time.Duration) Option {
	return func(c *CodeExecutor) { c.requestTimeout = t }
}

// WithExecutionTimeout sets the default per-block code execution
// timeout used by ExecuteCode. It also sets the floor for the request
// timeout (NewWithContext clamps requestTimeout to at least
// executionTimeout + requestTimeoutBuffer) so streaming /command
// calls can run for the full execution timeout.
func WithExecutionTimeout(t time.Duration) Option {
	return func(c *CodeExecutor) { c.executionTimeout = t }
}

// WithEnvVars sets environment variables injected into the sandbox at
// start.
func WithEnvVars(vars map[string]string) Option {
	return func(c *CodeExecutor) { c.envVars = vars }
}

// WithMetadata attaches metadata to the sandbox.
func WithMetadata(meta map[string]string) Option {
	return func(c *CodeExecutor) { c.metadata = meta }
}

// WithHTTPClient overrides the underlying HTTP client used by the
// OpenSandbox SDK. The client is passed through transparently to
// ConnectionConfig.HTTPClient.
func WithHTTPClient(h *http.Client) Option {
	return func(c *CodeExecutor) { c.httpClient = h }
}

// WithHeaders sets additional HTTP headers applied to every API call.
func WithHeaders(headers map[string]string) Option {
	return func(c *CodeExecutor) { c.headers = headers }
}

// WithSandboxID connects to an existing sandbox instead of creating a
// new one. Connected executors do not own the sandbox lifecycle:
// Close() will not kill it.
func WithSandboxID(sandboxID string) Option {
	return func(c *CodeExecutor) { c.sandboxID = sandboxID }
}

// WithUseServerProxy routes execd/egress HTTP requests through the
// OpenSandbox server instead of connecting directly to sandbox
// containers. Enable this when the client cannot reach sandbox
// containers directly — the canonical case is Docker Desktop on
// WSL2/macOS, where sandboxes live on a docker bridge network that is
// not routable from the host. Cloud-hosted OpenSandbox deployments
// (where each sandbox has a public endpoint) do not need this.
//
// This option maps to osb.ConnectionConfig.UseServerProxy.
func WithUseServerProxy(b bool) Option {
	return func(c *CodeExecutor) { c.useServerProxy = b }
}

// WithEndpointHostRewrite rewrites hostnames in endpoint URLs returned
// by the OpenSandbox server. This is needed when the server runs inside
// Docker and returns hostnames (e.g. "host.docker.internal") that the
// client cannot resolve — typically on a Linux host where
// host.docker.internal is not defined. The map's keys are the
// hostnames returned by the server; values are the replacements.
// Example: WithEndpointHostRewrite(map[string]string{"host.docker.internal": "localhost"}).
//
// The caller-provided map is used as-is; do not mutate it after passing
// it to New. This option maps to osb.ConnectionConfig.EndpointHostRewrite.
func WithEndpointHostRewrite(rewrites map[string]string) Option {
	return func(c *CodeExecutor) { c.endpointHostRewrite = rewrites }
}

// WithSandboxRunBase sets the base directory **inside the sandbox**
// where per-execution workspaces are created (default: /tmp/run).
func WithSandboxRunBase(dir string) Option {
	return func(c *CodeExecutor) { c.sandboxRunBase = dir }
}

// WorkspacePersistenceMode controls how long a sandbox workspace is
// reused.
type WorkspacePersistenceMode int

const (
	// WorkspacePersistencePerTurn creates a fresh workspace for each
	// turn. Files written during one turn are not visible to later
	// turns through the session workspace.
	WorkspacePersistencePerTurn WorkspacePersistenceMode = iota

	// WorkspacePersistencePerSession reuses one deterministic workspace
	// for all turns in the same session. Files written during one turn
	// remain visible to later turns in that session. In this mode
	// ExecuteCode and ExecuteInline do NOT auto-cleanup the workspace;
	// the caller is responsible for calling Cleanup when the session
	// ends.
	WorkspacePersistencePerSession
)

// WithWorkspacePersistence sets the workspace persistence mode. The
// default is WorkspacePersistencePerTurn. Use
// WorkspacePersistencePerSession when multi-turn agents should keep
// files and intermediate state across turns; in that mode the caller
// owns Cleanup (ExecuteCode/ExecuteInline skip auto-cleanup).
func WithWorkspacePersistence(mode WorkspacePersistenceMode) Option {
	return func(c *CodeExecutor) { c.workspacePersistence = mode }
}

// WithOutputPatterns sets the glob patterns used by Collect to harvest
// output files after ExecuteCode completes. Defaults to a sensible
// image/document set.
func WithOutputPatterns(patterns []string) Option {
	return func(c *CodeExecutor) { c.outputPatterns = patterns }
}

// CodeExecutor executes code inside an OpenSandbox sandbox.
//
// Lifecycle: CodeExecutor is not safe for concurrent use across the
// Close boundary. ExecuteCode / Sandbox / SandboxID may be called
// concurrently with each other, but Close must not run concurrently
// with any other method. This mirrors the e2b adapter's lifecycle
// contract.
type CodeExecutor struct {
	mu sync.Mutex

	// Connection-level options.
	apiKey              string
	domain              string
	protocol            string
	image               string
	entrypoint          []string
	resourceLimits      osb.ResourceLimits
	sandboxTimeout      time.Duration
	requestTimeout      time.Duration
	envVars             map[string]string
	metadata            map[string]string
	httpClient          *http.Client
	headers             map[string]string
	sandboxID           string
	useServerProxy      bool
	endpointHostRewrite map[string]string

	// Execution-level options.
	executionTimeout time.Duration
	outputPatterns   []string

	// Workspace integration (runs entirely inside the sandbox).
	sandboxRunBase       string
	workspacePersistence WorkspacePersistenceMode
	rt                   *workspaceRuntime

	// Sandbox instance.
	sbx *osb.Sandbox
	// owned indicates whether the CodeExecutor owns the sandbox
	// lifecycle (i.e., it created the sandbox itself and should kill
	// it on Close).
	owned bool
}

// requestTimeoutBuffer is the slack added on top of executionTimeout
// when clamping requestTimeout in NewWithContext. It absorbs the
// streaming /command overhead (init event, stdout/stderr framing,
// execution_complete) so the HTTP client does not kill a RunProgram
// call that finished just under the per-command execution timeout.
const requestTimeoutBuffer = 10 * time.Second

// defaultOutputPatterns is the default set of glob patterns used to
// collect output files after ExecuteCode completes.
var defaultOutputPatterns = []string{
	"*.png", "*.jpg", "*.jpeg", "*.gif", "*.svg",
	"*.csv", "*.json", "*.txt", "*.html", "*.pdf",
}

// New creates a new CodeExecutor. When WithSandboxID is supplied it
// connects to an existing sandbox; otherwise a new sandbox is created.
func New(opts ...Option) (*CodeExecutor, error) {
	return NewWithContext(context.Background(), opts...)
}

// NewWithContext is like New but accepts a context used for sandbox
// setup.
func NewWithContext(ctx context.Context, opts ...Option) (*CodeExecutor, error) {
	c := &CodeExecutor{
		image:            osb.CodeInterpreterImage,
		entrypoint:       osb.CodeInterpreterEntrypoint,
		sandboxTimeout:   time.Duration(osb.DefaultCodeInterpreterTimeoutSeconds) * time.Second,
		requestTimeout:   osb.DefaultRequestTimeout,
		executionTimeout: 30 * time.Second,
		outputPatterns:   defaultOutputPatterns,
	}
	for _, opt := range opts {
		opt(c)
	}

	// WithRequestTimeout(0) means "keep the SDK default"; resolve it
	// now so the clamp below and the RunProgram budget check both see
	// the actual timeout value rather than a sentinel 0 that would
	// silently bypass the budget check.
	if c.requestTimeout == 0 {
		c.requestTimeout = osb.DefaultRequestTimeout
	}

	// The OpenSandbox SDK applies ConnectionConfig.RequestTimeout to the
	// HTTP client used for ALL requests, including the streaming
	// /command endpoint used by RunProgram. If requestTimeout is shorter
	// than executionTimeout, a RunProgram call would be killed by the
	// HTTP client before the per-command execution timeout fires. Clamp
	// requestTimeout to at least executionTimeout + requestTimeoutBuffer
	// so streaming /command calls can run for the full execution timeout.
	effectiveExecTimeout := c.executionTimeout
	if effectiveExecTimeout <= 0 {
		effectiveExecTimeout = defaultRunTimeout
	}
	minRequestTimeout := effectiveExecTimeout + requestTimeoutBuffer
	if c.requestTimeout < minRequestTimeout {
		c.requestTimeout = minRequestTimeout
	}

	connCfg := osb.ConnectionConfig{
		Domain:              c.domain,
		Protocol:            c.protocol,
		APIKey:              c.apiKey,
		RequestTimeout:      c.requestTimeout,
		HTTPClient:          c.httpClient,
		Headers:             c.headers,
		UseServerProxy:      c.useServerProxy,
		EndpointHostRewrite: c.endpointHostRewrite,
	}

	createOpts := osb.SandboxCreateOptions{
		Image:          c.image,
		Entrypoint:     c.entrypoint,
		ResourceLimits: c.resourceLimits,
		Env:            c.envVars,
		Metadata:       c.metadata,
	}
	if c.sandboxTimeout > 0 {
		secs := int(c.sandboxTimeout / time.Second)
		createOpts.TimeoutSeconds = &secs
	}

	var (
		sbx *osb.Sandbox
		err error
	)
	if c.sandboxID != "" {
		sbx, err = osb.ConnectSandbox(ctx, connCfg, c.sandboxID)
		c.owned = false
	} else {
		sbx, err = osb.CreateSandbox(ctx, connCfg, createOpts)
		c.owned = true
	}
	if err != nil {
		return nil, fmt.Errorf("opensandbox: create/connect sandbox: %w", err)
	}
	c.sbx = sbx

	// Workspace runtime runs all file/program operations inside the
	// sandbox.
	c.rt = newWorkspaceRuntime(c)

	log.Debugf("opensandbox sandbox ready: id=%s", sbx.ID())
	return c, nil
}

// SandboxID returns the current sandbox id.
func (c *CodeExecutor) SandboxID() string {
	if c.sbx == nil {
		return ""
	}
	return c.sbx.ID()
}

// Sandbox exposes the underlying sandbox for advanced usage.
func (c *CodeExecutor) Sandbox() *osb.Sandbox { return c.sbx }

// CodeBlockDelimiter returns the fenced code delimiter.
func (c *CodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

// ExecuteCode executes all code blocks sequentially in the sandbox and
// aggregates their output. Each block is mapped via BuildBlockSpec to a
// filename and command, written into the workspace src/ subdirectory,
// then run via RunProgram. A BuildBlockSpec error or a non-zero exit
// code for one block is aggregated into the output and execution
// continues with the next block.
func (c *CodeExecutor) ExecuteCode(
	ctx context.Context, input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	if c.sbx == nil {
		return codeexecutor.CodeExecutionResult{}, fmt.Errorf(
			"opensandbox: sandbox not initialized",
		)
	}

	execID := input.ExecutionID
	if execID == "" {
		execID = fmt.Sprintf("exec_%d", time.Now().UnixNano())
	}

	ws, err := c.CreateWorkspace(ctx, execID, codeexecutor.WorkspacePolicy{})
	if err != nil {
		return codeexecutor.CodeExecutionResult{}, fmt.Errorf(
			"opensandbox: create workspace: %w", err,
		)
	}
	// In PerSession mode the workspace is reused across turns; the
	// caller owns cleanup. In PerTurn mode we clean up automatically.
	if c.workspacePersistence != WorkspacePersistencePerSession {
		defer c.Cleanup(ctx, ws)
	}

	var (
		out      strings.Builder
		outFiles []codeexecutor.File
	)
	for i, block := range input.CodeBlocks {
		fn, mode, cmd, args, err := codeexecutor.BuildBlockSpec(i, block)
		if err != nil {
			appendError(&out, err)
			continue
		}
		pf := codeexecutor.PutFile{
			Path:    path.Join(codeexecutor.InlineSourceDir, fn),
			Content: []byte(block.Code),
			Mode:    mode,
		}
		if err := c.PutFiles(ctx, ws, []codeexecutor.PutFile{pf}); err != nil {
			appendError(&out, err)
			continue
		}
		argv := append([]string{}, args...)
		argv = append(argv, path.Join(".", fn))
		res, err := c.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:     cmd,
			Args:    argv,
			Cwd:     codeexecutor.InlineSourceDir,
			Timeout: c.executionTimeout,
		})
		if err != nil {
			appendError(&out, err)
			continue
		}
		if res.Stdout != "" {
			out.WriteString(res.Stdout)
			if !strings.HasSuffix(res.Stdout, "\n") {
				out.WriteByte('\n')
			}
		}
		if res.Stderr != "" {
			appendStderr(&out, res.Stderr)
		}
		if res.ExitCode != 0 {
			fmt.Fprintf(&out, "[exit %d] %s\n", res.ExitCode, res.Stderr)
		}
	}

	files, err := c.Collect(ctx, ws, c.outputPatterns)
	if err != nil {
		// Collect is best-effort; surface the error but keep the
		// aggregated output.
		fmt.Fprintf(&out, "[collect error] %v\n", err)
	} else {
		outFiles = append(outFiles, files...)
	}

	return codeexecutor.CodeExecutionResult{
		Output:      out.String(),
		OutputFiles: outFiles,
	}, nil
}

// appendStderr writes a stderr chunk to the output buffer, prefixing
// each line so users can distinguish stderr from stdout.
func appendStderr(out *strings.Builder, line string) {
	if line == "" {
		return
	}
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

// appendError writes an error to the output buffer in a stable format.
func appendError(out *strings.Builder, err error) {
	if err == nil {
		return
	}
	out.WriteString("[error] ")
	out.WriteString(err.Error())
	if !strings.HasSuffix(err.Error(), "\n") {
		out.WriteByte('\n')
	}
}

// ensureRuntime returns the sandbox workspace runtime, lazily creating
// it for CodeExecutor instances that are used before a sandbox is
// attached.
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
//
// Not implemented in v1; returns errNotImplementedV1.
func (c *CodeExecutor) StageInputs(
	ctx context.Context, ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	return c.ensureRuntime().StageInputs(ctx, ws, specs)
}

// CollectOutputs applies the declarative output spec in the sandbox.
//
// Not implemented in v1; returns errNotImplementedV1.
func (c *CodeExecutor) CollectOutputs(
	ctx context.Context, ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return c.ensureRuntime().CollectOutputs(ctx, ws, spec)
}

// ExecuteInline writes inline code blocks into the sandbox and runs
// them.
func (c *CodeExecutor) ExecuteInline(
	ctx context.Context, execID string,
	blocks []codeexecutor.CodeBlock, timeout time.Duration,
) (codeexecutor.RunResult, error) {
	return c.ensureRuntime().ExecuteInline(ctx, execID, blocks, timeout)
}

// Engine exposes the sandbox-backed runtime as an Engine for skill
// tools.
//
// The engine advertises SupportsCleanEnv: RunProgram honors
// RunProgramSpec.CleanEnv by launching the spawned program through
// `env -i` with only the workspace base variables, the (already
// scrubbed) spec.Env and a minimal PATH, so the program does not
// inherit the sandbox process environment.
//
// Scope note: RunProgram wraps the user command in `bash -c` for
// stdin process substitution and stdout/stderr framing. That framing
// shell still inherits the sandbox environment because `env -i` is
// spliced into the inner command, not the outer `bash -c`. This is
// not a model-injection vector: the sandbox environment is fixed by
// the image / WithEnvVars at sandbox creation (operator-controlled),
// and model-supplied env (spec.Env) is confined to the `env -i`
// invocation rather than the framing shell. The SupportsCleanEnv
// contract is about the spawned program's environment, which `env -i`
// satisfies.
func (c *CodeExecutor) Engine() codeexecutor.Engine {
	rt := c.ensureRuntime()
	return codeexecutor.NewEngineWithCapabilities(
		rt, rt, rt,
		codeexecutor.Capabilities{SupportsCleanEnv: true},
	)
}

// Close terminates the owned sandbox (if any). Connected (non-owned)
// sandboxes are left running.
func (c *CodeExecutor) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sbx != nil && c.owned {
		if err := c.sbx.Kill(context.Background()); err != nil {
			log.Debugf("opensandbox: kill sandbox: %v", err)
			return err
		}
	}
	c.sbx = nil
	return nil
}

// errNotImplementedV1 is returned by v1 stub methods.
var errNotImplementedV1 = errors.New("opensandbox: not implemented in v1")
