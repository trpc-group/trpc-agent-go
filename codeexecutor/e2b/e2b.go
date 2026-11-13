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

// WithClientIDHostSuffix controls the legacy client ID suffix in sandbox
// hostnames. It is enabled by default. When enabled, the suffix is appended
// only when the response domain is empty and the client ID is non-empty.
// When disabled, fallback hostnames use only the sandbox ID and configured
// domain. A non-empty response domain always takes precedence without a suffix.
// This applies to Code Interpreter, native processes, and Sandbox().GetHost
// for both new and connected sandboxes.
func WithClientIDHostSuffix(enabled bool) Option {
	return func(c *CodeExecutor) { c.disableClientIDHostSuffix = !enabled }
}

// WithAPIURL overrides the full base URL of the E2B management API
// (e.g. "https://api.e2b.app" or "http://127.0.0.1:8080"). When set it
// takes precedence over WithDomain/WithDebug URL construction. Falls back
// to the E2B_API_URL env var when empty.
func WithAPIURL(apiURL string) Option {
	return func(c *CodeExecutor) { c.apiURL = apiURL }
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

// WithRequestTimeout sets the default HTTP request timeout.
// Zero uses the default of 30 seconds. A negative value disables that default
// deadline. For code execution it bounds the wait for response headers, not
// reading the execution stream or an HTTP error body, and it is not added to
// WithExecutionTimeout.
// An http.Client.Timeout on the configured client still covers the whole
// exchange, including a response body, and should be left unset or longer
// than streams that must stay open. RunCode reports that deadline separately
// from this option and records whether headers had already arrived. Increasing
// this option does not extend http.Client.Timeout.
func WithRequestTimeout(t time.Duration) Option {
	return func(c *CodeExecutor) { c.requestTimeout = t }
}

// WithExecutionTimeout sets the per-cell code execution deadline used by
// ExecuteCode. It bounds how long the client reads the response body after
// headers arrive, including the execution stream and an HTTP error body. Zero
// uses the default of 300 seconds. A negative value disables that body
// deadline and does not disable the request timeout.
// Ending the client wait does not stop the remote code or kill the sandbox.
// This option does not apply to RunProgram. An http.Client.Timeout is
// independent of this deadline; when it fires while the body is being read,
// RunCode does not report it as an execution TimeoutError.
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
// The connected sandbox is not owned by the executor. Combining it with
// WithAutoRecreate returns an error from New or NewWithContext.
func WithSandboxID(sandboxID string) Option {
	return func(c *CodeExecutor) { c.sandboxID = sandboxID }
}

// WithAutoRecreate controls on-demand replacement of an executor-owned sandbox.
// The default is false. False keeps the sandbox created or connected by New for
// every later call, including after that instance expires or disappears.
//
// WithAutoRecreate(true) and WithSandboxID together are rejected at
// construction. An externally supplied sandbox stays under its owner's control.
// Pause also releases ownership; a paused sandbox is not recreated.
//
// When the option is enabled, a later call may create one replacement from the
// template, environment variables, metadata, and connection settings captured
// at construction, including defaults resolved from the environment. The
// replacement is a new instance. Files, running processes, interpreter
// variables, and temporary data are not restored.
// Logical workspace IDs may be reused, but their directories, staged inputs,
// bootstrap, and workspace init hooks are applied again through the existing
// workspace registry. Paths created while this option is on include the
// process-local generation, so a handle from the previous instance does not
// name the replacement. The workspace instance ID also includes an identity
// unique to this executor, so two executors in one process do not share it.
// There is no background probe and no retry loop.
// Callers of one dead generation share a single creation attempt. A failed
// attempt stays retryable by a later call. The call that already reached the
// data plane is not replayed, and ExecuteCode does not run earlier blocks
// again. A replacement that loses to Close or Pause is killed. After a
// replacement is published, the owned sandbox it replaced is killed.
//
// Expiry time alone, a transport or execution timeout, a lost stream, an HTTP
// 5xx, and an arbitrary HTTP 404 do not prove that the sandbox was deleted.
// A code-interpreter HTTP 502 or 504, and an envd response reported as
// Connect unimplemented, only authorize one management check. Replacement is
// allowed only after that API reports that this sandbox resource is missing
// or that its state is terminal, such as killed or expired. Running and
// paused states are left in place.
//
// Sandbox returns the instance published at that moment. A held
// *codeinterpreter.Sandbox does not follow a later replacement. SandboxID
// reports the published instance and is empty when none is published.
func WithAutoRecreate(enabled bool) Option {
	return func(c *CodeExecutor) { c.autoRecreate = enabled }
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

// WorkspacePersistenceMode controls how long a sandbox workspace is reused.
type WorkspacePersistenceMode int

const (
	// WorkspacePersistencePerTurn creates a fresh workspace for each turn.
	// Files written during one turn are not visible to later turns through the
	// session workspace.
	WorkspacePersistencePerTurn WorkspacePersistenceMode = iota

	// WorkspacePersistencePerSession reuses one deterministic workspace for all
	// turns in the same session. Files written during one turn remain visible to
	// later turns in that session.
	WorkspacePersistencePerSession
)

// WithWorkspacePersistence sets the workspace persistence mode. The default is
// WorkspacePersistencePerTurn (a fresh workspace per CreateWorkspace call). Use
// WorkspacePersistencePerSession when multi-turn agents should keep files and
// intermediate state across turns.
func WithWorkspacePersistence(mode WorkspacePersistenceMode) Option {
	return func(c *CodeExecutor) { c.workspacePersistence = mode }
}

// CodeExecutor executes code inside an E2B code-interpreter sandbox.
type CodeExecutor struct {
	mu sync.Mutex

	// Connection-level options.
	disableClientIDHostSuffix bool

	apiKey         string
	accessToken    string
	domain         string
	apiURL         string
	debug          bool
	template       string
	sandboxTimeout time.Duration
	requestTimeout time.Duration
	envVars        map[string]string
	metadata       map[string]string
	httpClient     *http.Client
	headers        map[string]string
	sandboxID      string
	autoRecreate   bool

	// Execution-level options.
	executionTimeout time.Duration
	defaultLanguage  ci.RunCodeLanguage

	// Workspace integration (runs entirely inside the sandbox).
	sandboxRunBase       string
	workspacePersistence WorkspacePersistenceMode
	rt                   *workspaceRuntime

	// Sandbox instance.
	sbx           *ci.Sandbox
	createSandbox func(context.Context) (*ci.Sandbox, error)
	// executorID distinguishes this executor from others in the same process.
	// It is assigned once by New and is never reused.
	executorID uint64
	// generation identifies the published instance inside this executor.
	// It starts at 1 and increases only when auto-recreate publishes a
	// replacement. It is never reused, even if a provider reuses a sandbox ID.
	generation uint64
	// deadGeneration is the published generation confirmed gone by GetInfo.
	// Zero means the published generation is not awaiting replacement.
	deadGeneration uint64
	inflight       *recreateCall
	closing        *closeAttempt
	// closed is set for the duration of Close and stays set after a successful
	// close. A failed kill clears it so Close can be retried. Recovery checks
	// it before publishing so Close cannot be undone by a replacement.
	closed bool
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
// It copies environment, metadata, and header maps after applying the options,
// so later caller edits do not change the configuration used for replacements.
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
	if c.autoRecreate && c.sandboxID != "" {
		return nil, fmt.Errorf(
			"e2b: WithAutoRecreate cannot be combined with WithSandboxID",
		)
	}

	c.envVars = cloneStringMap(c.envVars)
	c.metadata = cloneStringMap(c.metadata)
	c.headers = cloneStringMap(c.headers)
	sbxOpts := c.sandboxOpts()

	var (
		sbx *ci.Sandbox
		err error
	)
	if c.sandboxID != "" {
		sbx, err = ci.Connect(ctx, c.sandboxID, sbxOpts)
		c.owned = false
	} else {
		c.createSandbox = ci.PrepareCreate(*sbxOpts)
		sbx, err = c.createSandbox(ctx)
		c.owned = true
	}
	if err != nil {
		return nil, fmt.Errorf("e2b: create/connect sandbox: %w", err)
	}
	c.sbx = sbx
	c.executorID = nextExecutorIdentity()
	c.generation = 1

	// Workspace runtime runs all file/program operations inside the sandbox.
	c.rt = newWorkspaceRuntime(c)

	log.Debugf("e2b sandbox ready: id=%s", sbx.SandboxID())
	return c, nil
}

// SandboxID returns the ID of the sandbox currently published by this executor.
// It is empty when no sandbox is published.
//
// The value is a snapshot of that publication. WithAutoRecreate can publish a
// different instance later; this call does not subscribe the caller to that
// change.
func (c *CodeExecutor) SandboxID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sbx == nil {
		return ""
	}
	return c.sbx.SandboxID()
}

// Sandbox returns the code-interpreter sandbox published for advanced use.
//
// The pointer is a snapshot. WithAutoRecreate may later publish a different
// instance; methods on a previously returned *codeinterpreter.Sandbox keep
// using the instance they already reference and are not redirected. Sandbox
// returns nil when no instance is published, including after Close releases it.
func (c *CodeExecutor) Sandbox() *ci.Sandbox {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sbx
}

// CodeBlockDelimiter returns the fenced code delimiter.
func (c *CodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

// ExecuteCode executes all code blocks sequentially in the sandbox and
// aggregates their output. Each block uses WithExecutionTimeout as its stream
// deadline and WithRequestTimeout as its header deadline. A timeout stops the
// client wait and returns an error; it does not kill the remote code or the
// sandbox.
func (c *CodeExecutor) ExecuteCode(
	ctx context.Context, input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	snap, err := c.useSandbox(ctx)
	if err != nil {
		return codeexecutor.CodeExecutionResult{}, err
	}

	var (
		out       strings.Builder
		outFiles  []codeexecutor.File
		fileIndex int
	)

	for i, block := range input.CodeBlocks {
		lang := pickLanguage(block.Language, c.defaultLanguage)
		exec, err := snap.sbx.RunCode(ctx, block.Code, &ci.RunCodeOpts{
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
			err = c.observeLoss(ctx, snap, err)
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

// RunProgram executes a command through native envd inside the sandbox workspace.
// A non-positive timeout uses 30 seconds. Non-zero program exits are returned
// in RunResult with a nil error; cancellation and transport failures are errors.
// TimedOut is set only for the envd process deadline and is identified from
// that error's type and chain. Caller cancellation, a caller deadline, and an
// unrelated failure whose text contains "timeout" do not set TimedOut.
// Recognized Linux signal termination returns exit code 128 + signal with a
// nil error, preserving captured output (for example, SIGTERM returns 143).
// Finite stdin requires a known envd version of at least 0.5.2. Output is
// captured without trimming or truncation. There is no Code Interpreter fallback.
// Remote programs default to root to match standard Code Interpreter workspace
// ownership. Custom templates should set Authorization through WithHeaders to
// select their kernel account; an explicit Authorization header takes precedence.
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
//
// The engine advertises SupportsCleanEnv: RunProgram honors
// RunProgramSpec.CleanEnv by launching the spawned program through
// `env -i` with only the workspace base variables, the (already
// scrubbed) spec.Env and a minimal PATH, so the program does not
// inherit the sandbox process environment (issue #1845). This lets
// tool/workspaceexec policy mode run on the e2b backend instead of
// failing closed.
//
// RunProgram uses native envd processes with separate stdout and stderr.
// ExecuteCode continues to use Code Interpreter kernels. Workspace filesystem
// operations still require the Code Interpreter endpoint.
func (c *CodeExecutor) Engine() codeexecutor.Engine {
	rt := c.ensureRuntime()
	var manager codeexecutor.WorkspaceManager = rt
	if c.autoRecreate {
		manager = &instanceWorkspaceRuntime{workspaceRuntime: rt}
	}
	return codeexecutor.NewEngineWithCapabilities(
		manager, rt, rt,
		codeexecutor.Capabilities{SupportsCleanEnv: true},
	)
}

// Pause suspends the sandbox without destroying it. The sandbox filesystem
// and memory state are preserved. Call New with WithSandboxID to resume.
// Only the owner of a sandbox (i.e. the executor that created it) should
// pause it; connected executors should leave lifecycle management to the
// creator.
// After a successful pause the executor relinquishes ownership so that a
// subsequent Close() call does not kill the paused sandbox. WithAutoRecreate
// does not install a replacement after that, including when creation is
// already in progress; that unpublished sandbox is killed.
func (c *CodeExecutor) Pause(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sbx == nil {
		return fmt.Errorf("e2b: sandbox not initialized")
	}
	if !c.owned {
		return fmt.Errorf("e2b: only owner may pause sandbox")
	}
	if err := c.sbx.Pause(ctx); err != nil {
		return err
	}
	// Detach ownership so Close() does not destroy the paused sandbox.
	// The caller can resume it later via New(WithSandboxID(...)).
	c.owned = false
	return nil
}

// Close releases the executor. When this executor owns the published sandbox,
// Close kills it. A failed kill leaves that sandbox published so Close can be
// retried. Concurrent callers share that one attempt and its result, so one
// caller cannot observe success while a failed attempt restores the sandbox.
// After Close has released the executor, recreation cannot publish a
// replacement; an instance created while Close is in progress is killed
// instead. A Close after a successful Close is a no-op.
func (c *CodeExecutor) Close() error {
	attempt, sbx, owned, wait := c.beginClose()
	if wait {
		<-attempt.done
		return attempt.err
	}
	if attempt == nil {
		return nil
	}
	var err error
	if sbx != nil && owned {
		err = sbx.Kill(context.Background())
	}
	c.finishClose(attempt, sbx, owned, err)
	return err
}
