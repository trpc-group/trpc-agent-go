//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package octool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultYieldMs         = 10_000
	defaultTimeoutS        = 1_800
	defaultLogTail         = 40
	defaultLogLimit        = 200
	defaultMaxLines        = 20_000
	defaultJobTTL          = 30 * time.Minute
	defaultKillGrace       = 2 * time.Second
	defaultIODrain         = 1 * time.Second
	defaultShellEnvTimeout = 5 * time.Second

	shellProgram     = "bash"
	shellLoginFlag   = "-lc"
	shellExitCleanup = `__trpc_claw_cleanup_jobs(){ ` +
		`local pids; pids="$(jobs -pr)"; ` +
		`if [ -n "$pids" ]; then ` +
		`kill $pids 2>/dev/null || true; ` +
		`sleep 0.05; ` +
		`kill -KILL $pids 2>/dev/null || true; ` +
		`fi; }; trap __trpc_claw_cleanup_jobs EXIT`
	shellEnvDumpCommand = "env -0"
)

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session

	maxLines             int
	jobTTL               time.Duration
	timeout              time.Duration
	maxTimeout           time.Duration
	maxYield             time.Duration
	baseEnv              map[string]string
	policy               CommandPolicy
	redactor             OutputRedactor
	maxResultOutputChars int

	clock func() time.Time

	shellEnvSnapshot func(context.Context, string) map[string]string
}

type Option func(*Manager)

func WithMaxLines(n int) Option {
	return func(m *Manager) {
		if n > 0 {
			m.maxLines = n
		}
	}
}

func WithJobTTL(d time.Duration) Option {
	return func(m *Manager) {
		if d > 0 {
			m.jobTTL = d
		}
	}
}

func WithDefaultTimeout(d time.Duration) Option {
	return func(m *Manager) {
		if d > 0 {
			m.timeout = d
		}
	}
}

// WithMaxTimeout caps command runtime, including timeout_sec requested by the
// model. Non-positive values preserve the legacy uncapped behavior.
func WithMaxTimeout(d time.Duration) Option {
	return func(m *Manager) {
		if d > 0 {
			m.maxTimeout = d
		}
	}
}

// WithMaxYield caps how long exec_command and write_stdin wait before returning
// interim output. Non-positive values preserve the legacy uncapped behavior.
func WithMaxYield(d time.Duration) Option {
	return func(m *Manager) {
		if d > 0 {
			m.maxYield = d
		}
	}
}

func WithBaseEnv(env map[string]string) Option {
	return func(m *Manager) {
		if len(env) == 0 {
			return
		}
		m.baseEnv = copyEnvMap(env)
	}
}

func WithCommandPolicy(policy CommandPolicy) Option {
	return func(m *Manager) {
		m.policy = policy
	}
}

func WithOutputRedactor(redactor OutputRedactor) Option {
	return func(m *Manager) {
		m.redactor = redactor
	}
}

// WithMaxResultOutputChars limits one-shot command output returned to the
// model. Non-positive values preserve the legacy unlimited behavior.
func WithMaxResultOutputChars(n int) Option {
	return func(m *Manager) {
		if n > 0 {
			m.maxResultOutputChars = n
		}
	}
}

func NewManager(opts ...Option) *Manager {
	m := &Manager{
		sessions:         map[string]*session{},
		maxLines:         defaultMaxLines,
		jobTTL:           defaultJobTTL,
		timeout:          time.Duration(defaultTimeoutS) * time.Second,
		clock:            time.Now,
		shellEnvSnapshot: snapshotLoginShellEnv,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

type execParams struct {
	Command    string
	Workdir    string
	Env        map[string]string
	Pty        bool
	Background bool

	YieldMs  *int
	TimeoutS *int
}

type execResult struct {
	Status     string   `json:"status"`
	Output     string   `json:"output,omitempty"`
	ExitCode   int      `json:"exitCode,omitempty"`
	SessionID  string   `json:"sessionId,omitempty"`
	MediaFiles []string `json:"media_files,omitempty"`
	MediaDirs  []string `json:"media_dirs,omitempty"`
}

func (m *Manager) Exec(
	ctx context.Context,
	params execParams,
) (execResult, error) {
	if ctx == nil {
		return execResult{}, errors.New("nil context")
	}
	if params.Command == "" {
		return execResult{}, errors.New("command is required")
	}
	var req CommandRequest
	if m.policy != nil || m.redactor != nil {
		req = m.commandRequest(ctx, params)
		if m.policy != nil {
			if err := m.policy(ctx, req); err != nil {
				return execResult{}, err
			}
		}
	}
	redact := m.outputRedactor(req)

	m.cleanupExpired()

	yieldMs := defaultYieldMs
	if params.YieldMs != nil && *params.YieldMs >= 0 {
		yieldMs = *params.YieldMs
	}
	yieldMs = m.clampYieldMs(yieldMs)

	timeout := m.commandTimeout(params.TimeoutS)

	if !params.Background && yieldMs == 0 && !params.Pty {
		out, code, err := runForeground(
			ctx,
			params,
			timeout,
			m.baseEnv,
		)
		if err != nil {
			return execResult{}, err
		}
		out = applyOutputRedactor(redact, out)
		out = m.limitResultOutput(out)
		return execResult{
			Status:   "exited",
			Output:   out,
			ExitCode: code,
		}, nil
	}

	sess, err := m.startBackground(ctx, params, timeout, redact)
	if err != nil {
		return execResult{}, err
	}

	if params.Background {
		return execResult{
			Status:    "running",
			SessionID: sess.id,
			Output:    m.limitTailResultOutput(sess.tail(defaultLogTail)),
		}, nil
	}

	if yieldMs == 0 {
		select {
		case <-ctx.Done():
			_ = m.kill(sess.id)
			return execResult{}, ctx.Err()
		case <-sess.doneCh:
		}
		out, code := sess.allOutput()
		_ = m.clearFinished(sess.id)
		out = m.limitResultOutput(out)
		return execResult{
			Status:   "exited",
			Output:   out,
			ExitCode: code,
		}, nil
	}

	yield := time.Duration(yieldMs) * time.Millisecond
	timer := time.NewTimer(yield)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		_ = m.kill(sess.id)
		return execResult{}, ctx.Err()
	case <-sess.doneCh:
		out, code := sess.allOutput()
		_ = m.clearFinished(sess.id)
		out = m.limitResultOutput(out)
		return execResult{
			Status:   "exited",
			Output:   out,
			ExitCode: code,
		}, nil
	case <-timer.C:
		return execResult{
			Status:    "running",
			SessionID: sess.id,
			Output:    m.limitTailResultOutput(sess.tail(defaultLogTail)),
		}, nil
	}
}

func runForeground(
	ctx context.Context,
	params execParams,
	timeout time.Duration,
	baseEnv map[string]string,
) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := shellCmd(ctx, params.Command, true)
	prepareCommandProcess(cmd)
	cmd.Dir = params.Workdir
	cmd.Env = mergedEnv(baseEnv, params.Env)
	defer func() {
		_ = cleanupCommandProcessGroup(cmd)
	}()

	out, err := cmd.CombinedOutput()
	code := exitCode(err)
	return string(out), code, nil
}

func shellCmd(
	ctx context.Context,
	command string,
	cleanupShellJobs bool,
) *exec.Cmd {
	if cleanupShellJobs {
		command = shellCommandWithExitCleanup(command)
	}
	cmd := exec.CommandContext(
		ctx,
		shellProgram,
		shellLoginFlag,
		command,
	)
	cmd.Cancel = func() error {
		return forceKillCommandProcess(cmd)
	}
	cmd.WaitDelay = defaultIODrain
	return cmd
}

func shellCommandWithExitCleanup(command string) string {
	return shellExitCleanup + "\n" + command
}

func mergedEnv(
	baseEnv map[string]string,
	extra map[string]string,
) []string {
	if len(baseEnv) == 0 && len(extra) == 0 {
		return nil
	}
	env := os.Environ()
	out := make([]string, 0, len(env)+len(baseEnv)+len(extra))
	out = append(out, env...)

	for k, v := range baseEnv {
		out = setEnv(out, k, v)
	}
	for k, v := range extra {
		out = setEnv(out, k, v)
	}
	return out
}

func setEnv(env []string, k, v string) []string {
	prefix := fmt.Sprintf("%s=", k)
	for i := range env {
		if strings.HasPrefix(env[i], prefix) {
			env[i] = prefix + v
			return env
		}
	}
	return append(env, prefix+v)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ProcessState != nil {
		return ee.ProcessState.ExitCode()
	}
	return -1
}

func (m *Manager) startBackground(
	parentCtx context.Context,
	params execParams,
	timeout time.Duration,
	redact func(string) string,
) (*session, error) {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	cmd := shellCmd(ctx, params.Command, !params.Background)
	cmd.Dir = params.Workdir
	cmd.Env = mergedEnv(m.baseEnv, params.Env)

	sess := newSession(newSessionID(), params.Command, m.maxLines)
	sess.cancel = cancel
	sess.redact = redact

	if params.Pty {
		master, closeIO, err := startPTY(cmd)
		if err != nil {
			cancel()
			return nil, err
		}
		sess.stdin = master
		sess.closeIO = closeIO
		sess.ioWG.Add(1)
		go func() {
			defer sess.ioWG.Done()
			sess.readFrom(master)
		}()
	} else {
		prepareCommandProcess(cmd)
		stdin, stdout, stderr, err := startPipes(cmd)
		if err != nil {
			cancel()
			return nil, err
		}
		sess.stdin = stdin
		sess.closeIO = func() error {
			_ = stdin.Close()
			_ = stdout.Close()
			_ = stderr.Close()
			return nil
		}
		sess.ioWG.Add(2)
		go func() {
			defer sess.ioWG.Done()
			sess.readFrom(stdout)
		}()
		go func() {
			defer sess.ioWG.Done()
			sess.readFrom(stderr)
		}()
		if err := cmd.Start(); err != nil {
			cancel()
			_ = sess.closeIO()
			return nil, err
		}
	}

	go func() {
		sess.ioWG.Wait()
		close(sess.ioDone)
	}()

	sess.cmd = cmd
	m.mu.Lock()
	m.sessions[sess.id] = sess
	m.mu.Unlock()

	go func() {
		// Use cmd.Process.Wait() instead of cmd.Wait() because
		// cmd.Wait() closes the pipe read ends returned by StdoutPipe
		// and StderrPipe, which races with readFrom goroutines still
		// reading from those pipes.  See the exec.StdoutPipe docs:
		// "It is thus incorrect to call Wait before all reads from the
		// pipe have completed."
		ps, _ := cmd.Process.Wait()
		waitDone(sess.ioDone, defaultIODrain)
		code := -1
		if ps != nil {
			code = ps.ExitCode()
		}
		sess.markDone(code)
		cancel()
		_ = sess.closeIO()
	}()

	return sess, nil
}

func copyEnvMap(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		if strings.TrimSpace(key) == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func (m *Manager) commandRequest(
	ctx context.Context,
	params execParams,
) CommandRequest {
	req := newCommandRequest(params)
	req.Env = m.commandEnv(
		ctx,
		params.Workdir,
		params.Env,
	)
	return req
}

func (m *Manager) commandEnv(
	ctx context.Context,
	workdir string,
	extra map[string]string,
) map[string]string {
	out := m.loginShellEnv(ctx, workdir)
	if len(out) == 0 {
		out = currentProcessEnvMap()
	}
	out = mergeEnvMaps(out, m.baseEnv)
	return mergeEnvMaps(out, extra)
}

func mergeEnvMaps(
	base map[string]string,
	extra map[string]string,
) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := copyEnvMap(base)
	if len(out) == 0 {
		out = make(map[string]string, len(extra))
	}
	for key, value := range extra {
		if strings.TrimSpace(key) == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func (m *Manager) outputRedactor(
	req CommandRequest,
) func(string) string {
	if m.redactor == nil {
		return nil
	}
	copied := copyCommandRequest(req)
	return func(output string) string {
		return m.redactor(copied, output)
	}
}

func copyCommandRequest(req CommandRequest) CommandRequest {
	req.Env = copyEnvMap(req.Env)
	return req
}

func applyOutputRedactor(
	redact func(string) string,
	output string,
) string {
	if redact == nil || output == "" {
		return output
	}
	return redact(output)
}

func (m *Manager) limitResultOutput(output string) string {
	if m == nil || m.maxResultOutputChars <= 0 {
		return output
	}
	return truncateResultOutput(output, m.maxResultOutputChars)
}

func (m *Manager) limitTailResultOutput(output string) string {
	if m == nil || m.maxResultOutputChars <= 0 {
		return output
	}
	return truncateTailResultOutput(output, m.maxResultOutputChars)
}

func (m *Manager) commandTimeout(timeoutS *int) time.Duration {
	timeout := m.timeout
	if timeoutS != nil && *timeoutS > 0 {
		timeout = time.Duration(*timeoutS) * time.Second
	}
	if timeout <= 0 {
		timeout = time.Duration(defaultTimeoutS) * time.Second
	}
	if m.maxTimeout > 0 && timeout > m.maxTimeout {
		return m.maxTimeout
	}
	return timeout
}

func (m *Manager) clampYieldMs(yieldMs int) int {
	if yieldMs <= 0 || m == nil || m.maxYield <= 0 {
		return yieldMs
	}
	maxMs := int(m.maxYield / time.Millisecond)
	if maxMs <= 0 {
		maxMs = 1
	}
	if yieldMs > maxMs {
		return maxMs
	}
	return yieldMs
}

func truncateResultOutput(output string, maxChars int) string {
	if maxChars <= 0 {
		return output
	}
	output = strings.ToValidUTF8(output, "\uFFFD")
	charCount := utf8.RuneCountInString(output)
	if charCount <= maxChars {
		return output
	}
	return appendTruncationNotice(
		firstRunes(output, maxChars),
		maxChars,
		charCount,
	)
}

func truncateLineWindowOutput(
	output string,
	maxChars int,
	offset int,
	nextOffset int,
) (string, int, bool) {
	if maxChars <= 0 {
		return output, nextOffset, false
	}
	output = strings.ToValidUTF8(output, "\uFFFD")
	charCount := utf8.RuneCountInString(output)
	if charCount <= maxChars {
		return output, nextOffset, false
	}
	if output == "" {
		return output, nextOffset, false
	}

	lines := strings.Split(output, "\n")
	parts := make([]string, 0, len(lines))
	keptChars := 0
	for _, line := range lines {
		addChars := utf8.RuneCountInString(line)
		if len(parts) > 0 {
			addChars++
		}
		if keptChars+addChars > maxChars {
			if len(parts) == 0 {
				prefix := firstRunes(line, maxChars)
				return appendTruncationNotice(
					prefix,
					utf8.RuneCountInString(prefix),
					charCount,
				), clampNextOffset(offset+1, offset, nextOffset), true
			}
			break
		}
		parts = append(parts, line)
		keptChars += addChars
	}

	consumed := len(parts)
	if consumed == 0 {
		prefix := firstRunes(output, maxChars)
		return appendTruncationNotice(
			prefix,
			utf8.RuneCountInString(prefix),
			charCount,
		), clampNextOffset(offset+1, offset, nextOffset), true
	}
	return appendTruncationNotice(
		strings.Join(parts, "\n"),
		keptChars,
		charCount,
	), clampNextOffset(offset+consumed, offset, nextOffset), true
}

func truncateTailResultOutput(output string, maxChars int) string {
	if maxChars <= 0 {
		return output
	}
	output = strings.ToValidUTF8(output, "\uFFFD")
	charCount := utf8.RuneCountInString(output)
	if charCount <= maxChars {
		return output
	}
	return fmt.Sprintf(
		"[OpenClaw truncated command output to the last %d of %d "+
			"chars. Write large outputs to a file and read only "+
			"the needed chunks with file tools or shell commands.]\n\n%s",
		maxChars,
		charCount,
		lastRunes(output, maxChars),
	)
}

func appendTruncationNotice(
	prefix string,
	keptChars int,
	totalChars int,
) string {
	return prefix + fmt.Sprintf(
		"\n\n[OpenClaw truncated command output to %d of %d chars. "+
			"Write large outputs to a file and read only the needed "+
			"chunks with file tools or shell commands.]",
		keptChars,
		totalChars,
	)
}

func clampNextOffset(next int, offset int, end int) int {
	if next <= offset && end > offset {
		next = offset + 1
	}
	if next > end {
		return end
	}
	return next
}

func firstRunes(value string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for idx := range value {
		if count == n {
			return value[:idx]
		}
		count++
	}
	return value
}

func lastRunes(value string, n int) string {
	if n <= 0 {
		return ""
	}
	start := utf8.RuneCountInString(value) - n
	if start <= 0 {
		return value
	}
	count := 0
	for idx := range value {
		if count == start {
			return value[idx:]
		}
		count++
	}
	return value
}

func currentProcessEnvMap() map[string]string {
	return envListToMap(os.Environ())
}

func envListToMap(env []string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for _, pair := range env {
		key, value, ok := splitEnvPair(pair)
		if !ok {
			continue
		}
		out[key] = value
	}
	return out
}

func splitEnvPair(pair string) (string, string, bool) {
	if pair == "" {
		return "", "", false
	}
	idx := strings.Index(pair, "=")
	if idx <= 0 {
		return "", "", false
	}
	return pair[:idx], pair[idx+1:], true
}

func (m *Manager) loginShellEnv(
	ctx context.Context,
	workdir string,
) map[string]string {
	snapshot := m.shellEnvSnapshot
	if snapshot == nil {
		snapshot = snapshotLoginShellEnv
	}
	return snapshot(ctx, workdir)
}

func snapshotLoginShellEnv(
	ctx context.Context,
	workdir string,
) map[string]string {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, defaultShellEnvTimeout)
	defer cancel()

	cmd := shellCmd(ctx, shellEnvDumpCommand, false)
	cmd.Dir = workdir

	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	pairs := bytes.Split(out, []byte{0})
	items := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if len(pair) == 0 {
			continue
		}
		items = append(items, string(pair))
	}
	return envListToMap(items)
}

func waitDone(done <-chan struct{}, timeout time.Duration) {
	if done == nil {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func startPipes(
	cmd *exec.Cmd,
) (io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, err
	}
	return stdin, stdout, stderr, nil
}

func (m *Manager) list() []processSession {
	m.cleanupExpired()

	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]processSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.snapshot())
	}
	sortSessions(out)
	return out
}

// ListSessions returns the current exec_command session snapshots.
func (m *Manager) ListSessions() []ProcessSession {
	return m.list()
}

func (m *Manager) poll(id string, limit *int) (processPoll, error) {
	s, err := m.get(id)
	if err != nil {
		return processPoll{}, err
	}
	return s.poll(limit, m.maxResultOutputChars), nil
}

func (m *Manager) log(
	id string,
	offset *int,
	limit *int,
) (processLog, error) {
	s, err := m.get(id)
	if err != nil {
		return processLog{}, err
	}
	return s.log(offset, limit, m.maxResultOutputChars), nil
}

func (m *Manager) write(
	id string,
	data string,
	newline bool,
) (processWrite, error) {
	s, err := m.get(id)
	if err != nil {
		return processWrite{}, err
	}
	return s.write(data, newline)
}

func (m *Manager) kill(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	return s.kill(defaultKillGrace)
}

func (m *Manager) clearFinished(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if s.running() {
		return errors.New("session is still running")
	}
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}

func (m *Manager) remove(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if s.running() {
		if err := s.kill(defaultKillGrace); err != nil {
			return err
		}
	}
	return m.clearFinished(id)
}

func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("unknown sessionId: %s", id)
	}
	return s, nil
}

func (m *Manager) cleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock()
	for id, s := range m.sessions {
		if s.running() {
			continue
		}
		if now.Sub(s.doneAt()) < m.jobTTL {
			continue
		}
		delete(m.sessions, id)
	}
}
