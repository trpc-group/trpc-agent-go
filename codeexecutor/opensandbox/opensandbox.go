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

// WithImage sets the sandbox container image URI. When empty (or when
// the option is not supplied) the SDK default CodeInterpreterImage is
// used; an explicit empty string is treated as "use the default" so
// callers cannot accidentally clear the image and trigger an SDK
// "missing image" error.
func WithImage(image string) Option {
	return func(c *CodeExecutor) {
		if image != "" {
			c.image = image
		}
	}
}

// WithEntrypoint overrides the sandbox entrypoint. When nil or empty
// the SDK default CodeInterpreterEntrypoint is used; an explicit empty
// slice is treated as "use the default" so callers cannot accidentally
// clear the entrypoint and fall through to tail -f /dev/null.
func WithEntrypoint(entrypoint []string) Option {
	return func(c *CodeExecutor) {
		if len(entrypoint) > 0 {
			c.entrypoint = entrypoint
		}
	}
}

// WithResourceLimits sets CPU/memory/GPU limits
// (e.g. {"cpu": "500m", "memory": "256Mi"}).
func WithResourceLimits(limits osb.ResourceLimits) Option {
	return func(c *CodeExecutor) { c.resourceLimits = limits }
}

// WithSandboxTimeout sets the wall-clock lifetime of the sandbox.
// Values in the range (0, 1s) are rejected because the OpenSandbox API
// only accepts integer seconds; a sub-second value would be silently
// truncated to 0, which the server may interpret as immediate expiry
// or no timeout. A value of 0 means "use the SDK default".
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
// start. The caller-provided map is copied so subsequent mutations to
// the original map do not affect the executor.
func WithEnvVars(vars map[string]string) Option {
	return func(c *CodeExecutor) {
		if vars == nil {
			c.envVars = nil
			return
		}
		copied := make(map[string]string, len(vars))
		for k, v := range vars {
			copied[k] = v
		}
		c.envVars = copied
	}
}

// WithMetadata attaches metadata to the sandbox. The caller-provided
// map is copied so subsequent mutations to the original map do not
// affect the executor.
func WithMetadata(meta map[string]string) Option {
	return func(c *CodeExecutor) {
		if meta == nil {
			c.metadata = nil
			return
		}
		copied := make(map[string]string, len(meta))
		for k, v := range meta {
			copied[k] = v
		}
		c.metadata = copied
	}
}

// WithHTTPClient overrides the underlying HTTP client used by the
// OpenSandbox SDK. NewWithContext shallow-copies the client before
// passing it to the SDK: the copy gets its own Timeout field (so the
// SDK's timeout configuration does not mutate the caller's client),
// while Transport is intentionally shared so the caller's connection
// pool and TLS config still apply.
func WithHTTPClient(h *http.Client) Option {
	return func(c *CodeExecutor) { c.httpClient = h }
}

// WithHeaders sets additional HTTP headers applied to every API call.
// The caller-provided map is copied so subsequent mutations to the
// original map do not affect the executor.
func WithHeaders(headers map[string]string) Option {
	return func(c *CodeExecutor) {
		if headers == nil {
			c.headers = nil
			return
		}
		copied := make(map[string]string, len(headers))
		for k, v := range headers {
			copied[k] = v
		}
		c.headers = copied
	}
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
// The caller-provided map is copied so subsequent mutations to the
// original map do not affect the executor. This option maps to
// osb.ConnectionConfig.EndpointHostRewrite.
func WithEndpointHostRewrite(rewrites map[string]string) Option {
	return func(c *CodeExecutor) {
		if rewrites == nil {
			c.endpointHostRewrite = nil
			return
		}
		copied := make(map[string]string, len(rewrites))
		for k, v := range rewrites {
			copied[k] = v
		}
		c.endpointHostRewrite = copied
	}
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
	//
	// Concurrent calls with the same session ID are NOT safe: they
	// reuse one workspace and will race on source files and output
	// directories. The caller must serialize calls sharing a session
	// ID.
	WorkspacePersistencePerSession
)

// WithWorkspacePersistence sets the workspace persistence mode. The
// default is WorkspacePersistencePerTurn. Use
// WorkspacePersistencePerSession when multi-turn agents should keep
// files and intermediate state across turns; in that mode the caller
// owns Cleanup (ExecuteCode/ExecuteInline skip auto-cleanup).
//
// PerSession mode is NOT safe for concurrent calls with the same
// session ID: they reuse one workspace and will race on source files
// and output directories. The caller must serialize calls sharing a
// session ID. PerTurn mode (the default) is safe for concurrent use.
func WithWorkspacePersistence(mode WorkspacePersistenceMode) Option {
	return func(c *CodeExecutor) { c.workspacePersistence = mode }
}

// WithOutputPatterns sets the glob patterns used by Collect to harvest
// output files after ExecuteCode completes. Defaults to a sensible
// image/document set. The caller's slice is copied so subsequent
// modifications do not affect the executor.
func WithOutputPatterns(patterns []string) Option {
	return func(c *CodeExecutor) {
		c.outputPatterns = append([]string(nil), patterns...)
	}
}

// CodeExecutor executes code inside an OpenSandbox sandbox.
//
// Lifecycle: CodeExecutor is not safe for concurrent use across the
// Close boundary. ExecuteCode / Sandbox / SandboxID may be called
// concurrently with each other, but Close must not run concurrently
// with any other method. This mirrors the e2b adapter's lifecycle
// contract.
//
// Concurrency with WorkspacePersistencePerSession: when the executor
// is configured with PerSession persistence, calls sharing the same
// session ID reuse one workspace and therefore MUST be serialized by
// the caller. Concurrent ExecuteCode/ExecuteInline calls with the same
// session ID will race on source files (src/inline_0.*), output
// directories, and run directories, causing cross-request
// interference. PerTurn mode (the default) is safe for concurrent use
// because each call gets an isolated workspace.
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
		outputPatterns:   append([]string(nil), defaultOutputPatterns...),
	}
	for _, opt := range opts {
		opt(c)
	}

	// Validate the configured runBase before creating or connecting to
	// a sandbox. Without this early check, an invalid runBase (e.g.
	// "/tmp/run/../../etc") would cause CreateSandbox to succeed, then
	// validateRunBase to fail, and the caller — unable to obtain the
	// CodeExecutor to call Close() — would leak the sandbox until the
	// server-side timeout fires.
	if err := validateRunBase(c.sandboxRunBase); err != nil {
		return nil, err
	}

	// Validate sandbox-level env var names for contract consistency
	// with envToken's validation of spec.Env. WithEnvVars does not go
	// through bash -c concatenation (the SDK serializes Env as JSON),
	// so this is not a shell-injection defense; however, rejecting
	// invalid names here keeps the two env-entry paths consistent and
	// prevents a future refactor that reuses c.envVars in a command
	// string from reintroducing the U1 injection vector.
	for k := range c.envVars {
		if !validEnvName(k) {
			return nil, fmt.Errorf(
				"opensandbox: invalid environment variable name %q in WithEnvVars "+
					"(must match [A-Za-z_][A-Za-z0-9_]*)", k,
			)
		}
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

	// Clone the caller-provided *http.Client before handing it to the
	// SDK. The SDK's WithTimeout option writes c.httpClient.Timeout in
	// place when the client has a custom Transport (it only clones when
	// Transport is nil). Without cloning, the SDK would mutate the
	// caller's shared client, changing timeout behaviour for unrelated
	// auth/proxy/mesh traffic reusing the same client in this process.
	// The shallow copy is sufficient: Timeout is a value field (so the
	// clone gets its own), and Transport is intentionally shared (the
	// caller's connection pool / TLS config still applies).
	var httpClient *http.Client
	if c.httpClient != nil {
		cloned := *c.httpClient
		httpClient = &cloned
	}
	connCfg := osb.ConnectionConfig{
		Domain:              c.domain,
		Protocol:            c.protocol,
		APIKey:              c.apiKey,
		RequestTimeout:      c.requestTimeout,
		HTTPClient:          httpClient,
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
		if c.sandboxTimeout < time.Second {
			return nil, fmt.Errorf(
				"opensandbox: sandbox timeout %v must be at least 1s",
				c.sandboxTimeout,
			)
		}
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
		if c.workspacePersistence == WorkspacePersistencePerSession {
			// In PerSession mode, a random fallback would produce a
			// different workspace hash on every call, defeating
			// persistence and leaking workspaces (PerSession skips
			// auto-cleanup). Require the caller to provide a stable
			// session-derived ID.
			return codeexecutor.CodeExecutionResult{}, errors.New(
				"opensandbox: ExecutionID must not be empty when using " +
					"WorkspacePersistencePerSession; provide a stable " +
					"session-derived ID so the workspace can be reused " +
					"across turns",
			)
		}
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
	// Use a context detached from the parent's cancellation so cleanup
	// still runs after the parent context is cancelled/timed out.
	if c.workspacePersistence != WorkspacePersistencePerSession {
		defer func() {
			cleanupCtx, cancel := cleanupContext(ctx)
			defer cancel()
			if err := c.Cleanup(cleanupCtx, ws); err != nil {
				log.Errorf("opensandbox: cleanup workspace %q: %v", ws.Path, err)
			}
		}()
	}

	var (
		out      cappedOutputBuffer
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
		if res.TimedOut {
			fmt.Fprintf(&out, "[timeout: execution exceeded %s]\n", c.executionTimeout)
		}
		if res.ExitCode != 0 && !res.TimedOut {
			// Don't repeat stderr here — it was already written via
			// appendStderr above. Only add the exit status line.
			fmt.Fprintf(&out, "[exit %d]\n", res.ExitCode)
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
func appendStderr(out *cappedOutputBuffer, line string) {
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
func appendError(out *cappedOutputBuffer, err error) {
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
// Not implemented in v1; returns ErrNotImplementedV1. Callers can
// detect this with errors.Is(err, ErrNotImplementedV1) and fall back
// to PutFiles.
func (c *CodeExecutor) StageInputs(
	ctx context.Context, ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	return c.ensureRuntime().StageInputs(ctx, ws, specs)
}

// CollectOutputs applies the declarative output spec in the sandbox.
//
// Not implemented in v1; returns ErrNotImplementedV1. Callers can
// detect this with errors.Is(err, ErrNotImplementedV1) and fall back
// to Collect.
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
// Scope note: when CleanEnv is requested, RunProgram wraps the outer
// `bash -c` in `env -i PATH=... bash --norc --noprofile -c '...'` so
// the framing shell itself starts with a minimal environment. This
// prevents BASH_ENV (if set in the sandbox env) from sourcing an
// arbitrary file and LD_PRELOAD from being loaded before the inner
// `env -i` command runs. When CleanEnv is false, the outer bash
// inherits the sandbox environment as before.
func (c *CodeExecutor) Engine() codeexecutor.Engine {
	rt := c.ensureRuntime()
	return codeexecutor.NewEngineWithCapabilities(
		rt, rt, rt,
		codeexecutor.Capabilities{SupportsCleanEnv: true},
	)
}

// killTimeout bounds the Kill call in Close so that a sandbox whose
// DELETE endpoint is hung (e.g. server-side deadlock, network
// partition) does not block Close indefinitely. 30s matches
// defaultRmTimeout — long enough for a clean server-side teardown,
// short enough not to hang the agent process.
const killTimeout = 30 * time.Second

// Close terminates the owned sandbox (if any). Connected (non-owned)
// sandboxes are left running.
//
// Kill uses context.WithTimeout(context.Background(), killTimeout)
// rather than context.Background() alone: a bare Background context
// has no deadline, so a hung DELETE /v1/sandboxes/{id} would block
// Close forever, leaking the goroutine and any deferred Close callers
// above it (e.g. agent shutdown).
func (c *CodeExecutor) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sbx != nil && c.owned {
		killCtx, cancel := context.WithTimeout(context.Background(), killTimeout)
		defer cancel()
		if err := c.sbx.Kill(killCtx); err != nil {
			log.Debugf("opensandbox: kill sandbox: %v", err)
			return err
		}
	}
	c.sbx = nil
	return nil
}

// ErrNotImplementedV1 is returned by the v1 stub implementations of
// StageInputs and CollectOutputs on CodeExecutor (the direct methods).
// Callers can detect it with errors.Is(err, ErrNotImplementedV1) and
// fall back to PutFiles / Collect.
//
// When accessed via Engine().FS(), the gatingFS wrapper installed by
// NewEngineWithCapabilities intercepts StageInputs/CollectOutputs and
// returns codeexecutor.ErrDeclarativeIONotSupported instead, because
// this engine does not advertise SupportsDeclarativeIO. Cross-package
// callers that use the Engine interface should check
// errors.Is(err, codeexecutor.ErrDeclarativeIONotSupported).
var ErrNotImplementedV1 = errors.New("opensandbox: not implemented in v1")

// errNotImplementedV1 is retained as a package-private alias for
// backward compatibility with existing test assertions and the
// StageInputs/CollectOutputs stubs in workspace_collect.go.
var errNotImplementedV1 = ErrNotImplementedV1
