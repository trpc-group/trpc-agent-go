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
//
// # Platform support
//
// Directory staging (PutDirectory / StageDirectory) pins the host tree
// with openat(2) + O_NOFOLLOW to close the host-directory swap race. On
// Linux, BSD, and Darwin this is implemented natively. On Windows and
// other non-Unix hosts the source tree cannot be pinned, so directory
// staging fail-closes with an error instead of reopening children by
// pathname (which would leave the swap race open). As a result, skill
// staging and other directory uploads are unavailable when the agent
// process runs on a Windows host. Single-file uploads (PutFiles) and
// program execution are unaffected. The sandbox itself always runs
// remotely, so the host OS never changes sandbox-side behavior.
//
// # v1 surface
//
// This package is a per-turn executor: each ExecuteCode / ExecuteInline
// call gets a fresh workspace that is deleted when the call returns.
// Session-scoped workspaces, filesystem-safe session keys, and
// declarative I/O (StageInputs / CollectOutputs) are out of scope.
// Those methods exist to satisfy WorkspaceFS but return an unsupported
// error; callers that need skill Inputs, workspace init hooks, or
// SaveArtifact should keep using PutFiles / Collect until a follow-up
// lands the shared root-module contracts. workspaceexec policy mode is
// refused because the engine advertises SupportsCleanEnv: false.
package opensandbox

import (
	"context"
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
//
// The selected image must provide a POSIX shell accessible as `bash`
// (invoked via `bash -c`) and the GNU coreutils `readlink -z -m`,
// `base64 -w0` and `tr`, which the workspace runtime invokes when
// staging files and resolving working directories (resolved paths are
// base64 framed so a path containing a newline survives the SDK's
// line-based stdout stream). Output collection does not shell out: it
// uses execd's directory listing and download endpoints, so the execd
// build in the image must report lstat file types in listings.
// Minimal or Alpine busybox images that lack these tools will construct
// a sandbox successfully but fail at the first staging operation. Stick
// to the default CodeInterpreterImage unless you can guarantee these
// tools are present.
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
			c.entrypoint = append([]string(nil), entrypoint...)
		}
	}
}

// ResourceLimits defines sandbox runtime resource constraints as
// key-value pairs, mirroring the OpenSandbox server schema without
// leaking the SDK type. Values are Kubernetes-style quantity strings:
//
//	"cpu":    CPU share, e.g. "500m" (0.5 vCPU) or "2" (2 vCPUs)
//	"memory": byte limit with binary-SI suffix, e.g. "256Mi", "2Gi"
//	"gpu":    GPU count, e.g. "1"
//
// The map is passed through to the OpenSandbox create-sandbox API; see
// the OpenSandbox documentation for the full set of supported keys.
// When nil or empty the server default applies (cpu "1", memory "2Gi").
type ResourceLimits map[string]string

// WithResourceLimits sets sandbox-level CPU/memory/GPU limits using
// Kubernetes-style quantity strings, e.g.
// ResourceLimits{"cpu": "500m", "memory": "256Mi"} — "cpu" is in
// millicores ("500m" = 0.5 vCPU, "2" = 2 vCPUs), "memory" is a byte
// quantity with binary-SI suffix ("256Mi", "2Gi"), and "gpu" is a
// plain count ("1"). Limits only take effect when a new sandbox is
// created; they are ignored when connecting via WithSandboxID. When
// not supplied (or nil), the OpenSandbox server default applies
// (cpu "1", memory "2Gi").
func WithResourceLimits(limits ResourceLimits) Option {
	return func(c *CodeExecutor) {
		if limits == nil {
			c.resourceLimits = nil
			return
		}
		copied := make(ResourceLimits, len(limits))
		for k, v := range limits {
			copied[k] = v
		}
		c.resourceLimits = copied
	}
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
// runs. Set t to 0 to use the default (defaultRequestTimeout, sized to
// cover the framework-standard 5-minute tool run timeout), which is
// then clamped like any other value.
func WithRequestTimeout(t time.Duration) Option {
	return func(c *CodeExecutor) { c.requestTimeout = t }
}

// WithExecutionTimeout sets the default per-block code execution
// timeout used by ExecuteCode. It also sets the floor for the request
// timeout (NewWithContext clamps requestTimeout to at least
// executionTimeout + requestTimeoutBuffer) so streaming /command
// calls can run for the full execution timeout.
//
// Compatibility: per-command timeout enforcement requires execd
// v1.0.18 or later. Older execd versions (including v1.0.3) ignore
// the SDK's timeout field, so RunResult.TimedOut may never be set
// even when WithExecutionTimeout is configured. Ensure the deployed
// OpenSandbox server meets this minimum version before relying on
// timeout semantics.
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
// Close boundary. ExecuteCode / SandboxID may be called concurrently
// with each other, but Close must not run concurrently with any other
// method. This mirrors the e2b adapter's lifecycle contract. Each
// ExecuteCode / ExecuteInline call runs in an isolated per-call
// workspace, so concurrent calls are safe.
type CodeExecutor struct {
	mu sync.Mutex

	// Connection-level options.
	apiKey              string
	domain              string
	protocol            string
	image               string
	entrypoint          []string
	resourceLimits      ResourceLimits
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
	sandboxRunBase string
	rt             *workspaceRuntime

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

// defaultRequestTimeout is the default HTTP request timeout. It is
// sized to cover the framework-standard 5-minute RunProgram timeout
// used by tool/skill and tool/workspaceexec
// (defaultSkillRunTimeout / defaultWorkspaceExecTimeout) plus the
// streaming buffer, so those tools work against OpenSandbox without
// extra configuration. Callers needing a different cap can set
// WithRequestTimeout explicitly; note the value only bounds how long a
// hung request may stall — fast requests are unaffected.
const defaultRequestTimeout = 5*time.Minute + requestTimeoutBuffer

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
		requestTimeout:   defaultRequestTimeout,
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

	// WithRequestTimeout(0) means "keep the default"; resolve it
	// now so the clamp below and the RunProgram budget check both see
	// the actual timeout value rather than a sentinel 0 that would
	// silently bypass the budget check.
	if c.requestTimeout == 0 {
		c.requestTimeout = defaultRequestTimeout
	}

	// The OpenSandbox SDK applies ConnectionConfig.RequestTimeout to the
	// HTTP client used for ALL requests, including the streaming
	// /command endpoint used by RunProgram. If requestTimeout is shorter
	// than executionTimeout, a RunProgram call would be killed by the
	// HTTP client before the per-command execution timeout fires. Clamp
	// requestTimeout to at least executionTimeout + requestTimeoutBuffer
	// so streaming /command calls can run for the full execution timeout.
	//
	// Persist the resolved default back into c.executionTimeout so that
	// ExecuteCode's timeout marker ("[timeout: execution exceeded %s]")
	// reports the actual run timeout rather than the sentinel 0s or a
	// negative value the caller may have configured.
	if c.executionTimeout <= 0 {
		c.executionTimeout = defaultRunTimeout
	}
	effectiveExecTimeout := c.executionTimeout
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
		ResourceLimits: osb.ResourceLimits(c.resourceLimits),
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

// snapshotSandbox returns the current sandbox pointer under the
// lifecycle lock. Close mutates c.sbx; every other reader must go
// through this helper so a concurrent Close cannot race the pointer
// even though overlapping Close with in-flight work is unsupported.
func (c *CodeExecutor) snapshotSandbox() *osb.Sandbox {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sbx
}

// SandboxID returns the current sandbox id.
func (c *CodeExecutor) SandboxID() string {
	sbx := c.snapshotSandbox()
	if sbx == nil {
		return ""
	}
	return sbx.ID()
}

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
	if c.snapshotSandbox() == nil {
		return codeexecutor.CodeExecutionResult{}, fmt.Errorf(
			"opensandbox: sandbox not initialized",
		)
	}

	ws, err := c.ensureRuntime().CreateWorkspace(ctx, input.ExecutionID, codeexecutor.WorkspacePolicy{})
	if err != nil {
		return codeexecutor.CodeExecutionResult{}, fmt.Errorf(
			"opensandbox: create workspace: %w", err,
		)
	}
	// Each ExecuteCode call gets a fresh per-call workspace; clean it up
	// when the call finishes. Use a context detached from the parent's
	// cancellation so cleanup still runs after the parent context is
	// cancelled/timed out.
	defer func() {
		cleanupCtx, cancel := cleanupContext(ctx)
		defer cancel()
		if err := c.Cleanup(cleanupCtx, ws); err != nil {
			log.Errorf("opensandbox: cleanup workspace %q: %v", ws.Path, err)
		}
	}()

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
// Not implemented in v1; returns errDeclarativeIONotSupported (a
// package-private sentinel — check the error message or simply treat
// any error from this method as "unsupported") and callers should fall
// back to PutFiles.
func (c *CodeExecutor) StageInputs(
	ctx context.Context, ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	return c.ensureRuntime().StageInputs(ctx, ws, specs)
}

// CollectOutputs applies the declarative output spec in the sandbox.
//
// Not implemented in v1; returns errDeclarativeIONotSupported (a
// package-private sentinel, see StageInputs); callers should fall back
// to Collect.
func (c *CodeExecutor) CollectOutputs(
	ctx context.Context, ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return c.ensureRuntime().CollectOutputs(ctx, ws, spec)
}

// ExecuteInline writes inline code blocks into the sandbox and runs
// them. Each call runs in a fresh per-call workspace that is cleaned up
// when the call finishes.
func (c *CodeExecutor) ExecuteInline(
	ctx context.Context, execID string,
	blocks []codeexecutor.CodeBlock, timeout time.Duration,
) (codeexecutor.RunResult, error) {
	return c.ensureRuntime().ExecuteInline(ctx, execID, blocks, timeout)
}

// Engine exposes the sandbox-backed runtime as an Engine for skill
// tools.
//
// The engine does NOT advertise SupportsCleanEnv. RunProgram still
// best-effort prefixes env -i for the remote command string, but
// OpenSandbox execd always starts that string via shell -c with an
// environment merged from the sandbox process (os.Environ). Client-
// side prefixes therefore cannot form a trustworthy CleanEnv security
// boundary for tool/workspaceexec policy mode.
//
// TODO: re-audit SupportsCleanEnv when execd adds clean-env support
// (e.g. a RunCommandRequest flag that prevents os.Environ merge into
// the outer shell). Flip to true here and add a test verifying the
// outer shell does not inherit host env.
//
// Declarative I/O (StageInputs/CollectOutputs) is not supported in
// v1: the runtime stubs return errDeclarativeIONotSupported, so
// callers should fall back to PutFiles/Collect.
func (c *CodeExecutor) Engine() codeexecutor.Engine {
	rt := c.ensureRuntime()
	return codeexecutor.NewEngineWithCapabilities(
		rt, rt, rt,
		codeexecutor.Capabilities{
			// SupportsCleanEnv is false: OpenSandbox execd launches
			// commands as shell -c with env merged from the sandbox
			// process os.Environ() (execd command.go). Client-side
			// env -i cannot prevent BASH_ENV/LD_PRELOAD on that outer
			// shell, so advertising true would mislead workspaceexec.
			SupportsCleanEnv: false,
		},
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
