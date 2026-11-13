//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2b

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	ci "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/codeinterpreter"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const discardedSandboxKillTimeout = 15 * time.Second

const workspaceInstanceSegment = "inst"

var errExecutorClosed = errors.New("e2b: executor is closed")

// executorIdentitySeq assigns each constructed executor an ID that is unique
// for the life of this process. The number is not a sandbox ID.
var executorIdentitySeq atomic.Uint64

func nextExecutorIdentity() uint64 {
	return executorIdentitySeq.Add(1)
}

type closeAttempt struct {
	done chan struct{}
	err  error
}

// Recovery runs only after GetInfo for the current sandbox reports that the
// sandbox resource is missing or that its state is terminal. A transport
// timeout, an EndAt timestamp, a data-plane 404, and any other non-confirming
// response leave the published instance in place. HTTP 502 and 504 on the
// code-interpreter route, and Connect CodeUnimplemented from the envd route,
// only authorize that GetInfo check. One creation is shared by callers
// waiting on the same dead generation. The call that already reached the
// data plane is not replayed. A published replacement replaces the owned
// sandbox, which is then killed. Close and Pause discard an unpublished
// replacement instead of installing it.

// recreateWaiters counts callers blocked in waitRecreate. Tests use it to
// cancel a waiter only after it is waiting.
var recreateWaiters atomic.Int32

type sandboxSnap struct {
	sbx        *ci.Sandbox
	generation uint64
	owned      bool
}

type sandboxSnapKey struct{}

type recreateCall struct {
	generation uint64
	done       chan struct{}
	err        error
}

type instanceWorkspaceRuntime struct {
	*workspaceRuntime
}

func (r *instanceWorkspaceRuntime) InstanceID(
	ctx context.Context,
) (codeexecutor.WorkspaceInstanceID, error) {
	if r == nil || r.workspaceRuntime == nil || r.ce == nil {
		return "", errors.New("e2b: sandbox not initialized")
	}
	return r.ce.instanceID(ctx)
}

func (c *CodeExecutor) sandboxOpts() *ci.SandboxOpts {
	return &ci.SandboxOpts{
		DisableClientIDHostSuffix: c.disableClientIDHostSuffix,
		APIKey:                    c.apiKey,
		AccessToken:               c.accessToken,
		Domain:                    c.domain,
		APIURL:                    c.apiURL,
		Debug:                     c.debug,
		RequestTimeout:            c.requestTimeout,
		Timeout:                   c.sandboxTimeout,
		Template:                  c.template,
		Metadata:                  c.metadata,
		EnvVars:                   c.envVars,
		HTTPClient:                c.httpClient,
		Headers:                   c.headers,
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (c *CodeExecutor) currentSnap() (sandboxSnap, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.sbx == nil {
		return sandboxSnap{}, errors.New("e2b: sandbox not initialized")
	}
	return sandboxSnap{
		sbx:        c.sbx,
		generation: c.generation,
		owned:      c.owned,
	}, nil
}

func (c *CodeExecutor) useSandbox(ctx context.Context) (sandboxSnap, error) {
	snap, err := c.prepareInstance(ctx)
	if err != nil {
		return sandboxSnap{}, err
	}
	// A missing sandbox keeps its existing error even when ctx is already
	// canceled. Cancellation applies once an instance is available and before
	// the caller starts work on it.
	if err := ctx.Err(); err != nil {
		return sandboxSnap{}, err
	}
	return snap, nil
}

func (c *CodeExecutor) prepareInstance(ctx context.Context) (sandboxSnap, error) {
	snap, err := c.currentSnap()
	if err != nil {
		return sandboxSnap{}, err
	}
	if c.autoRecreate && snap.owned && c.generationIsDead(snap.generation) {
		if err := c.recover(ctx, snap.generation); err != nil {
			return sandboxSnap{}, err
		}
		return c.currentSnap()
	}
	if err := c.waitPublished(ctx, snap.generation); err != nil {
		return sandboxSnap{}, err
	}
	return c.currentSnap()
}

func (c *CodeExecutor) instanceID(
	ctx context.Context,
) (codeexecutor.WorkspaceInstanceID, error) {
	snap, err := c.prepareInstance(ctx)
	if err != nil {
		return "", err
	}
	if c.executorID == 0 || snap.generation == 0 {
		return "", errors.New("e2b: sandbox instance is unavailable")
	}
	return codeexecutor.WorkspaceInstanceID(
		strconv.FormatUint(c.executorID, 10) +
			":" + strconv.FormatUint(snap.generation, 10),
	), nil
}

func (c *CodeExecutor) generationIsDead(generation uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return generation != 0 && c.deadGeneration == generation
}

func (c *CodeExecutor) markDead(generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.generation != generation {
		return
	}
	c.deadGeneration = generation
}

func (c *CodeExecutor) stillOwned(generation uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.owned && !c.closed && c.generation == generation
}

func (c *CodeExecutor) waitPublished(ctx context.Context, generation uint64) error {
	c.mu.Lock()
	call := c.inflight
	if call == nil || call.generation != generation {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	return waitRecreate(ctx, call)
}

func (c *CodeExecutor) recover(ctx context.Context, deadGen uint64) error {
	call, lead, err := c.beginRecover(deadGen)
	if err != nil || call == nil {
		return err
	}
	if !lead {
		return waitRecreate(ctx, call)
	}
	return c.leadRecover(ctx, deadGen, call)
}

func (c *CodeExecutor) beginRecover(
	deadGen uint64,
) (*recreateCall, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, false, errExecutorClosed
	}
	if !c.autoRecreate || !c.owned {
		return nil, false, errors.New(
			"e2b: sandbox is not owned by this executor",
		)
	}
	if c.generation != deadGen {
		return nil, false, nil
	}
	if c.inflight != nil {
		if c.inflight.generation != deadGen {
			return nil, false, errors.New("e2b: sandbox recovery conflict")
		}
		return c.inflight, false, nil
	}
	call := &recreateCall{
		generation: deadGen,
		done:       make(chan struct{}),
	}
	c.inflight = call
	return call, true, nil
}

func (c *CodeExecutor) leadRecover(
	ctx context.Context,
	deadGen uint64,
	call *recreateCall,
) error {
	sbx, err := c.createSandbox(ctx)
	discard := c.completeRecover(deadGen, call, sbx, err)
	if discard != nil {
		killDiscardedSandbox(ctx, discard)
	}
	return call.err
}

func (c *CodeExecutor) completeRecover(
	deadGen uint64,
	call *recreateCall,
	sbx *ci.Sandbox,
	err error,
) *ci.Sandbox {
	c.mu.Lock()
	defer c.mu.Unlock()
	discard := c.publishRecoverLocked(deadGen, call, sbx, err)
	if c.inflight == call {
		c.inflight = nil
	}
	close(call.done)
	return discard
}

func (c *CodeExecutor) publishRecoverLocked(
	deadGen uint64,
	call *recreateCall,
	sbx *ci.Sandbox,
	err error,
) *ci.Sandbox {
	if c.closed || !c.owned || c.generation != deadGen {
		return c.abandonRecoverLocked(call, sbx)
	}
	if err != nil || sbx == nil {
		call.err = err
		if call.err == nil {
			call.err = errors.New("e2b: sandbox recreation returned no sandbox")
		}
		return nil
	}
	previous := c.sbx
	c.sbx = sbx
	c.generation++
	c.owned = true
	if c.deadGeneration == deadGen {
		c.deadGeneration = 0
	}
	log.Debugf("e2b sandbox recreated: id=%s", sbx.SandboxID())
	if previous == nil || previous == sbx {
		return nil
	}
	return previous
}

// abandonRecoverLocked drops a creation that lost the right to publish.
// The caller kills a non-nil sandbox after releasing the executor lock.
func (c *CodeExecutor) abandonRecoverLocked(
	call *recreateCall,
	sbx *ci.Sandbox,
) *ci.Sandbox {
	call.err = nil
	if c.closed {
		call.err = errExecutorClosed
	}
	return sbx
}

func waitRecreate(ctx context.Context, call *recreateCall) error {
	recreateWaiters.Add(1)
	defer recreateWaiters.Add(-1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-call.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return call.err
	}
}

func killDiscardedSandbox(ctx context.Context, sbx *ci.Sandbox) {
	killCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		discardedSandboxKillTimeout,
	)
	defer cancel()
	if err := sbx.Kill(killCtx); err != nil {
		log.Debugf("e2b: kill unreferenced sandbox: %v", err)
	}
}

// beginClose starts one close attempt or joins the attempt already running.
// A nil attempt with wait false means Close already succeeded.
func (c *CodeExecutor) beginClose() (
	attempt *closeAttempt, sbx *ci.Sandbox, owned bool, wait bool,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing != nil {
		return c.closing, nil, false, true
	}
	if c.closed && c.sbx == nil {
		return nil, nil, false, false
	}
	attempt = &closeAttempt{done: make(chan struct{})}
	c.closing = attempt
	sbx, owned = c.sbx, c.owned
	c.closed = true
	c.sbx = nil
	c.owned = false
	return attempt, sbx, owned, false
}

func (c *CodeExecutor) finishClose(
	attempt *closeAttempt, sbx *ci.Sandbox, owned bool, err error,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.closed = false
		if c.sbx == nil {
			c.sbx = sbx
			c.owned = owned
		}
		log.Debugf("e2b: kill sandbox: %v", err)
	}
	attempt.err = err
	if c.closing == attempt {
		c.closing = nil
	}
	close(attempt.done)
}

func (c *CodeExecutor) observeLoss(
	ctx context.Context,
	snap sandboxSnap,
	err error,
) error {
	if !c.shouldConfirmLoss(snap, err) {
		return c.lossResult(snap, err, false)
	}
	dead, confirmErr := confirmSandboxDead(ctx, snap.sbx)
	if confirmErr != nil || !dead {
		if ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}
		return c.lossResult(snap, err, false)
	}
	c.markDead(snap.generation)
	recErr := c.recover(ctx, snap.generation)
	if recErr != nil {
		log.Debugf("e2b: sandbox recreation: %v", recErr)
	}
	wrapped := c.lossResult(snap, err, true)
	if ctx.Err() != nil || errors.Is(recErr, errExecutorClosed) {
		return errors.Join(wrapped, ctx.Err(), recErr)
	}
	return wrapped
}

func (c *CodeExecutor) lossResult(
	snap sandboxSnap,
	err error,
	confirmedDead bool,
) error {
	// Close or Pause can discard a replacement even after creation succeeds.
	recreated := c.generationMoved(snap)
	if !confirmedDead && !recreated {
		return err
	}
	var notice error
	if recreated {
		notice = errors.New("e2b: sandbox was recreated. " +
			"Check the required files and runtime environment before continuing")
	}
	return errors.Join(
		err,
		codeexecutor.ErrWorkspaceStale,
		codeexecutor.ErrWorkspaceRetryUnsafe,
		notice,
	)
}

func (c *CodeExecutor) generationMoved(snap sandboxSnap) bool {
	if c == nil || !c.autoRecreate || snap.generation == 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.generation != snap.generation
}

func (c *CodeExecutor) shouldConfirmLoss(snap sandboxSnap, err error) bool {
	if err == nil || !c.autoRecreate || snap.sbx == nil || !lossCandidate(err) {
		return false
	}
	return c.stillOwned(snap.generation)
}

func confirmSandboxDead(ctx context.Context, sbx *ci.Sandbox) (bool, error) {
	info, err := sbx.GetInfo(ctx)
	if err != nil {
		var notFound *ci.NotFoundError
		if errors.As(err, &notFound) {
			return true, nil
		}
		return false, err
	}
	if info == nil {
		return false, nil
	}
	return terminalSandboxState(info.State), nil
}

func terminalSandboxState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "killed", "deleted", "expired", "terminated", "destroyed", "dead":
		return true
	default:
		return false
	}
}

func lossCandidate(err error) bool {
	if err == nil || definiteNonLoss(err) {
		return false
	}
	return signalsPossibleSandboxLoss(err)
}

func definiteNonLoss(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return typedNonLoss(err)
}

func typedNonLoss(err error) bool {
	if gatewayTimeout(err) {
		return false
	}
	var timeout *ci.TimeoutError
	if errors.As(err, &timeout) {
		return true
	}
	var auth *ci.AuthenticationError
	if errors.As(err, &auth) {
		return true
	}
	var rate *ci.RateLimitError
	if errors.As(err, &rate) {
		return true
	}
	var invalid *ci.InvalidArgumentError
	if errors.As(err, &invalid) {
		return true
	}
	var sandboxErr *ci.SandboxError
	if errors.As(err, &sandboxErr) {
		return true
	}
	return false
}

func signalsPossibleSandboxLoss(err error) bool {
	var notFound *ci.NotFoundError
	if errors.As(err, &notFound) {
		return true
	}
	if gatewayTimeout(err) || connectLoss(err) || networkLoss(err) {
		return true
	}
	return false
}

func gatewayTimeout(err error) bool {
	var timeout *ci.TimeoutError
	if !errors.As(err, &timeout) || timeout == nil {
		return false
	}
	switch timeout.StatusCode {
	case http.StatusBadGateway, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func connectLoss(err error) bool {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return false
	}
	switch connectErr.Code() {
	case connect.CodeNotFound, connect.CodeUnavailable, connect.CodeUnimplemented:
		return true
	default:
		return false
	}
}

func networkLoss(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return !urlErr.Timeout()
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

func withSandboxSnap(ctx context.Context, snap sandboxSnap) context.Context {
	return context.WithValue(ctx, sandboxSnapKey{}, snap)
}

func sandboxSnapFrom(ctx context.Context) (sandboxSnap, bool) {
	if ctx == nil {
		return sandboxSnap{}, false
	}
	snap, ok := ctx.Value(sandboxSnapKey{}).(sandboxSnap)
	if !ok || snap.sbx == nil {
		return sandboxSnap{}, false
	}
	return snap, true
}

func (r *workspaceRuntime) bindWorkspace(
	ctx context.Context,
	ws codeexecutor.Workspace,
) (sandboxSnap, error) {
	if r == nil || r.ce == nil {
		return sandboxSnap{}, errors.New("e2b: sandbox not initialized")
	}
	snap, err := r.ce.useSandbox(ctx)
	if err != nil {
		return sandboxSnap{}, err
	}
	if !r.ce.autoRecreate {
		return snap, nil
	}
	generation, ok := r.workspaceGeneration(ws.Path)
	if !ok || generation != snap.generation {
		return sandboxSnap{}, codeexecutor.ErrWorkspaceStale
	}
	return snap, nil
}

func (r *workspaceRuntime) bindContext(
	ctx context.Context,
	ws codeexecutor.Workspace,
) (context.Context, error) {
	snap, err := r.bindWorkspace(ctx, ws)
	if err != nil {
		return ctx, err
	}
	return withSandboxSnap(ctx, snap), nil
}

func (r *workspaceRuntime) workspaceGeneration(wsPath string) (uint64, bool) {
	if r == nil || r.ce == nil || !r.ce.autoRecreate {
		return 0, false
	}
	clean := path.Clean(wsPath)
	parent := path.Join(r.cfg.runBase, workspaceInstanceSegment)
	prefix := parent + "/"
	if !strings.HasPrefix(clean, prefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(clean, prefix)
	genText, _, _ := strings.Cut(rest, "/")
	generation, err := strconv.ParseUint(genText, 10, 64)
	if err != nil || generation == 0 {
		return 0, false
	}
	return generation, true
}

func (r *workspaceRuntime) allocateWorkspacePath(
	execID string,
	generation uint64,
) (string, error) {
	if r.cfg.workspacePersistence == WorkspacePersistencePerSession && execID == "" {
		return "", errors.New(
			"e2b: execID must not be empty when using WorkspacePersistencePerSession",
		)
	}
	leaf := perTurnWorkspaceLeaf(execID)
	if r.cfg.workspacePersistence == WorkspacePersistencePerSession {
		leaf = "ws_" + stableWorkspaceHash(execID)
	}
	if r.ce != nil && r.ce.autoRecreate && generation != 0 {
		return path.Join(
			r.cfg.runBase,
			workspaceInstanceSegment,
			strconv.FormatUint(generation, 10),
			leaf,
		), nil
	}
	return path.Join(r.cfg.runBase, leaf), nil
}

func perTurnWorkspaceLeaf(execID string) string {
	return "ws_" + sanitize(execID) + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)
}
