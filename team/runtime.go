//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package team

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/sessionroute"
	itransfer "trpc.group/trpc-go/trpc-agent-go/internal/transfer"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type swarmRuntime struct {
	mu           sync.Mutex
	teamName     string
	entryName    string
	cfg          SwarmConfig
	handoff      swarmHandoffPolicy
	inputBuilder SwarmHandoffInputBuilder
	handoffs     int
	recent       []string
	sessions     map[string]*session.Session
	branches     map[string]*session.Session
}

func (sr *swarmRuntime) OnTransfer(
	_ context.Context,
	fromAgent string,
	toAgent string,
) (time.Duration, error) {
	_ = fromAgent
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.handoffs++
	if sr.cfg.MaxHandoffs > 0 && sr.handoffs > sr.cfg.MaxHandoffs {
		return 0, fmt.Errorf(
			"max handoffs exceeded: %d",
			sr.cfg.MaxHandoffs,
		)
	}
	window := sr.cfg.RepetitiveHandoffWindow
	minUnique := sr.cfg.RepetitiveHandoffMinUnique
	if window > 0 && minUnique > 0 {
		sr.recent = append(sr.recent, toAgent)
		if len(sr.recent) > window {
			sr.recent = sr.recent[len(sr.recent)-window:]
		}
		if len(sr.recent) == window && uniqueCount(sr.recent) < minUnique {
			return 0, errRepetitiveHandoff
		}
	}
	return sr.cfg.NodeTimeout, nil
}

func (sr *swarmRuntime) CustomizeTransferInvocation(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
) error {
	if target == nil {
		return nil
	}
	if sr.handoff.usesIsolatedSession() {
		if err := sr.isolateTargetSession(ctx, source, target); err != nil {
			return err
		}
	}
	sr.registerInvocationSession(target.InvocationID, target.Branch, target.Session)
	if sr.inputBuilder == nil {
		return nil
	}
	transferMessage := target.Message.Content
	if rawTransferMessage, ok := itransfer.TransferMessageFromContext(ctx); ok {
		transferMessage = rawTransferMessage
	}
	msg, err := sr.inputBuilder(ctx, SwarmHandoffInputArgs{
		FromAgentName:   sourceAgentName(source),
		ToAgentName:     target.AgentName,
		RootInput:       rootMessage(source),
		ParentInput:     sourceMessage(source),
		TransferMessage: transferMessage,
	})
	if err != nil {
		return err
	}
	target.Message = normalizeHandoffInputMessage(msg)
	return nil
}

func (sr *swarmRuntime) isolateTargetSession(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
) error {
	root := rootSession(source)
	if root == nil {
		return errors.New("root session is nil")
	}
	if target.SessionService == nil {
		return errors.New("target session service is nil")
	}
	sess, err := sr.sessionForTransferTarget(ctx, target, root)
	if err != nil {
		return err
	}
	target.Session = sess
	return nil
}

func (sr *swarmRuntime) sessionForAgentStart(
	ctx context.Context,
	service session.Service,
	root *session.Session,
	agentName string,
) (*session.Session, error) {
	switch sr.handoff.normalizedSessionScope() {
	case swarmSessionScopeShared:
		return root, nil
	default:
		return sr.perAgentSession(ctx, service, root, agentName)
	}
}

func (sr *swarmRuntime) sessionForTransferTarget(
	ctx context.Context,
	target *agent.Invocation,
	root *session.Session,
) (*session.Session, error) {
	switch sr.handoff.normalizedSessionScope() {
	case swarmSessionScopeShared:
		return target.Session, nil
	default:
		return sr.perAgentSession(ctx, target.SessionService, root, target.AgentName)
	}
}

func (sr *swarmRuntime) perAgentSession(
	ctx context.Context,
	service session.Service,
	root *session.Session,
	toAgent string,
) (*session.Session, error) {
	if root == nil {
		return nil, errors.New("root session is nil")
	}
	if service == nil {
		return nil, errors.New("session service is nil")
	}
	if toAgent == "" {
		return nil, errors.New("target agent name is empty")
	}
	if toAgent == sr.entryName {
		return root, nil
	}
	return sr.newIsolatedSession(ctx, service, root, toAgent)
}

func (sr *swarmRuntime) newIsolatedSession(
	ctx context.Context,
	service session.Service,
	root *session.Session,
	toAgent string,
) (*session.Session, error) {
	if root == nil {
		return nil, errors.New("root session is nil")
	}
	if service == nil {
		return nil, errors.New("session service is nil")
	}
	if toAgent == "" {
		return nil, errors.New("target agent name is empty")
	}
	sessionID := sr.isolatedSessionID(root, toAgent)
	if sessionID == "" {
		return nil, errors.New("isolated session id is empty")
	}
	return sr.getOrCreateSession(ctx, service, root, sessionID)
}

func (sr *swarmRuntime) getOrCreateSession(
	ctx context.Context,
	service session.Service,
	root *session.Session,
	sessionID string,
) (*session.Session, error) {
	key := session.Key{
		AppName:   root.AppName,
		UserID:    root.UserID,
		SessionID: sessionID,
	}
	sess, err := service.GetSession(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get isolated session: %w", err)
	}
	if sess != nil {
		return sess, nil
	}
	sess, err = service.CreateSession(ctx, key, session.StateMap{})
	if err != nil {
		existing, getErr := service.GetSession(ctx, key)
		if getErr == nil && existing != nil {
			return existing, nil
		}
		return nil, fmt.Errorf("create isolated session: %w", err)
	}
	return sess, nil
}

func (sr *swarmRuntime) isolatedSessionID(
	root *session.Session,
	toAgent string,
) string {
	return defaultSwarmSessionID(swarmSessionIDArgs{
		ParentSessionID: root.ID,
		TeamName:        sr.teamName,
		EntryAgentName:  sr.entryName,
		ToAgentName:     toAgent,
	})
}

func (sr *swarmRuntime) OnTransferComplete(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
	targetEvent *event.Event,
) {
	sr.saveTransferOwner(ctx, source, target, targetEvent)
}

func (sr *swarmRuntime) OnTransferTerminalError(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
	targetEvent *event.Event,
) {
	sr.saveTransferOwner(ctx, source, target, targetEvent)
}

func (sr *swarmRuntime) saveTransferOwner(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
	targetEvent *event.Event,
) {
	if !sr.handoff.targetTakesOver() || target == nil || targetEvent == nil {
		return
	}
	root := rootSession(source)
	if root == nil {
		return
	}
	teamName := sr.teamName
	if teamName == "" {
		teamNameBytes, ok := root.GetState(SwarmTeamNameKey)
		if !ok || len(teamNameBytes) == 0 {
			return
		}
		teamName = string(teamNameBytes)
	}
	activeAgentKey := swarmActiveAgentKey(teamName)
	activeAgentValue := []byte(target.AgentName)
	state := session.StateMap{activeAgentKey: activeAgentValue}
	root.SetState(activeAgentKey, activeAgentValue)
	if sr.handoff.usesIsolatedSession() {
		routeState, err := sessionroute.CurrentTurnRouteState(
			teamName,
			target.AgentName,
			root,
			target.Session,
		)
		if err != nil {
			log.DebugfContext(ctx, "save swarm current turn route skipped: %v", err)
		}
		for key, value := range routeState {
			state[key] = value
		}
		sessionroute.ApplyCurrentTurnRouteState(root, routeState)
	}
	if sameSession(root, target.Session) {
		if targetEvent.StateDelta == nil {
			targetEvent.StateDelta = make(map[string][]byte)
		}
		targetEvent.StateDelta[activeAgentKey] = activeAgentValue
		if !sr.handoff.usesIsolatedSession() {
			if itransfer.IsSyntheticCompletionEvent(targetEvent) {
				updateRootSessionState(ctx, source, root, state)
			}
			return
		}
	}
	updateRootSessionState(ctx, source, root, state)
}

func updateRootSessionState(
	ctx context.Context,
	source *agent.Invocation,
	root *session.Session,
	state session.StateMap,
) {
	if source == nil || source.SessionService == nil {
		return
	}
	key := session.Key{
		AppName:   root.AppName,
		UserID:    root.UserID,
		SessionID: root.ID,
	}
	if err := source.SessionService.UpdateSessionState(ctx, key, state); err != nil {
		log.WarnfContext(ctx, "save active swarm agent state skipped or failed: %v", err)
	}
}

func (sr *swarmRuntime) registerInvocationSession(
	invocationID string,
	branch string,
	sess *session.Session,
) {
	if sr == nil || sess == nil {
		return
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if invocationID == "" && branch == "" {
		return
	}
	if sr.sessions == nil {
		sr.sessions = make(map[string]*session.Session)
	}
	if invocationID != "" {
		sr.sessions[invocationID] = sess
	}
	if branch == "" {
		return
	}
	if sr.branches == nil {
		sr.branches = make(map[string]*session.Session)
	}
	sr.branches[branch] = sess
}

func (sr *swarmRuntime) sessionForEvent(
	evt *event.Event,
) (*session.Session, bool) {
	if sr == nil || evt == nil {
		return nil, false
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if len(sr.sessions) == 0 && len(sr.branches) == 0 {
		return nil, false
	}
	if sess, ok := sr.sessions[evt.InvocationID]; ok && sess != nil {
		return sess, true
	}
	if sess, ok := sr.sessions[evt.ParentInvocationID]; ok && sess != nil {
		if evt.InvocationID != "" {
			sr.sessions[evt.InvocationID] = sess
		}
		return sess, true
	}
	if sess, ok := sr.sessionForBranchLocked(evt.Branch); ok && sess != nil {
		return sess, true
	}
	return nil, false
}

func (sr *swarmRuntime) sessionForBranchLocked(
	branch string,
) (*session.Session, bool) {
	var (
		bestSession *session.Session
		bestLen     int
	)
	for prefix, sess := range sr.branches {
		if prefix == "" || !branchMatchesPrefix(branch, prefix) {
			continue
		}
		if len(prefix) > bestLen {
			bestSession = sess
			bestLen = len(prefix)
		}
	}
	return bestSession, bestLen > 0
}

func branchMatchesPrefix(branch string, prefix string) bool {
	return branch == prefix || strings.HasPrefix(branch, prefix+agent.BranchDelimiter)
}

// RouteEvent routes isolated member events to their member session.
func (sr *swarmRuntime) RouteEvent(
	root *agent.Invocation,
	routeEvt *event.Event,
) (*session.Session, bool) {
	if sr == nil || !sr.handoff.usesIsolatedSession() || root == nil || routeEvt == nil {
		return nil, false
	}
	sess, ok := sr.sessionForEvent(routeEvt)
	if !ok || sameSession(root.Session, sess) {
		return nil, false
	}
	return sess, true
}

func sourceAgentName(source *agent.Invocation) string {
	if source == nil {
		return ""
	}
	return source.AgentName
}

func sourceMessage(source *agent.Invocation) model.Message {
	if source == nil {
		return model.Message{}
	}
	return source.Message
}

func rootMessage(source *agent.Invocation) model.Message {
	msg := sourceMessage(source)
	for current := source; current != nil; current = current.GetParentInvocation() {
		if model.HasPayload(current.Message) {
			msg = current.Message
		}
	}
	return msg
}

func rootSession(inv *agent.Invocation) *session.Session {
	var fallback *session.Session
	for current := inv; current != nil; current = current.GetParentInvocation() {
		if current.Session == nil {
			continue
		}
		fallback = current.Session
		if _, ok := current.Session.GetState(SwarmTeamNameKey); ok {
			return current.Session
		}
	}
	return fallback
}

func sameSession(a *session.Session, b *session.Session) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.AppName == b.AppName && a.UserID == b.UserID && a.ID == b.ID
}

func (t *Team) prepareSwarmStartSession(
	ctx context.Context,
	invocation *agent.Invocation,
	startAgent agent.Agent,
	swarmRun *swarmRuntime,
) (*session.Session, error) {
	startSession := invocation.Session
	currentTurnRouteMissing := t.swarmHandoff.usesIsolatedSession() &&
		t.swarmHandoff.targetTakesOver() &&
		!sessionroute.HasCurrentTurnRoute(t.name, invocation.Session)
	if t.swarmHandoff.usesIsolatedSession() {
		sess, err := swarmRun.sessionForAgentStart(
			ctx,
			invocation.SessionService,
			invocation.Session,
			startAgent.Info().Name,
		)
		if err != nil {
			return nil, err
		}
		startSession = sess
	}
	if currentTurnRouteMissing && !sameSession(invocation.Session, startSession) {
		if err := appendCurrentTurnUserEvents(ctx, invocation, startSession); err != nil {
			return nil, err
		}
	}
	if swarmRun != nil && t.swarmHandoff.usesIsolatedSession() {
		sessionroute.AttachEventRouter(invocation, swarmRun)
	}
	return startSession, nil
}

func appendCurrentTurnUserEvents(
	ctx context.Context,
	invocation *agent.Invocation,
	target *session.Session,
) error {
	if invocation == nil || invocation.SessionService == nil || target == nil || sameSession(invocation.Session, target) {
		return nil
	}
	for _, evt := range currentTurnUserEvents(invocation) {
		if err := invocation.SessionService.AppendEvent(ctx, target, evt); err != nil {
			return err
		}
	}
	return nil
}

func currentTurnUserEvents(invocation *agent.Invocation) []*event.Event {
	if invocation == nil || invocation.Session == nil {
		return nil
	}
	events := make([]*event.Event, 0)
	invocation.Session.EventMu.RLock()
	defer invocation.Session.EventMu.RUnlock()
	for i := range invocation.Session.Events {
		evt := invocation.Session.Events[i]
		if evt.InvocationID != invocation.InvocationID || !evt.IsUserMessage() {
			continue
		}
		events = append(events, evt.Clone())
	}
	return events
}

func normalizeHandoffInputMessage(msg model.Message) model.Message {
	if msg.Role == "" && model.HasPayload(msg) {
		msg.Role = model.RoleUser
	}
	return msg
}

func ensureSwarmRuntime(
	inv *agent.Invocation,
	teamName string,
	entryName string,
	cfg SwarmConfig,
	handoff swarmHandoffPolicy,
	inputBuilder SwarmHandoffInputBuilder,
) *swarmRuntime {
	if inv == nil {
		return nil
	}
	cloneRuntimeStateForSwarm(&inv.RunOptions)
	runtime := &swarmRuntime{
		teamName:     teamName,
		entryName:    entryName,
		cfg:          cfg,
		handoff:      handoff,
		inputBuilder: inputBuilder,
	}
	installSwarmTransferController(&inv.RunOptions, runtime)
	return runtime
}

func cloneRuntimeStateForSwarm(opts *agent.RunOptions) {
	if opts == nil {
		return
	}
	cloned := make(map[string]any, len(opts.RuntimeState)+1)
	for key, value := range opts.RuntimeState {
		cloned[key] = value
	}
	opts.RuntimeState = cloned
}

var (
	errRepetitiveHandoff = errors.New("repetitive handoff detected")
)

func uniqueCount(values []string) int {
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		seen[v] = struct{}{}
	}
	return len(seen)
}

func installSwarmTransferController(opts *agent.RunOptions, next agent.TransferController) {
	if opts == nil || next == nil {
		return
	}
	if opts.RuntimeState == nil {
		opts.RuntimeState = make(map[string]any)
	}
	existing, _ := agent.GetRuntimeStateValue[agent.TransferController](
		opts,
		agent.RuntimeStateKeyTransferController,
	)
	opts.RuntimeState[agent.RuntimeStateKeyTransferController] = composeTransferControllers(
		stripSwarmTransferControllers(existing),
		next,
	)
}

func stripSwarmTransferControllers(controller agent.TransferController) agent.TransferController {
	switch c := controller.(type) {
	case nil:
		return nil
	case *swarmRuntime:
		return nil
	case chainedTransferController:
		return composeTransferControllers(
			stripSwarmTransferControllers(c.first),
			stripSwarmTransferControllers(c.second),
		)
	default:
		return controller
	}
}

func composeTransferControllers(
	first agent.TransferController,
	second agent.TransferController,
) agent.TransferController {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return chainedTransferController{first: first, second: second}
}

type chainedTransferController struct {
	first  agent.TransferController
	second agent.TransferController
}

func (c chainedTransferController) OnTransfer(
	ctx context.Context,
	fromAgent string,
	toAgent string,
) (time.Duration, error) {
	firstTimeout, err := c.first.OnTransfer(ctx, fromAgent, toAgent)
	if err != nil {
		return 0, err
	}
	secondTimeout, err := c.second.OnTransfer(ctx, fromAgent, toAgent)
	if err != nil {
		return 0, err
	}
	return tighterTimeout(firstTimeout, secondTimeout), nil
}

func (c chainedTransferController) CustomizeTransferInvocation(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
) error {
	if first, ok := c.first.(itransfer.InvocationCustomizer); ok && first != nil {
		if err := first.CustomizeTransferInvocation(ctx, source, target); err != nil {
			return err
		}
	}
	if second, ok := c.second.(itransfer.InvocationCustomizer); ok && second != nil {
		return second.CustomizeTransferInvocation(ctx, source, target)
	}
	return nil
}

func (c chainedTransferController) OnTransferComplete(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
	targetEvent *event.Event,
) {
	if first, ok := c.first.(itransfer.CompletionObserver); ok && first != nil {
		first.OnTransferComplete(ctx, source, target, targetEvent)
	}
	if second, ok := c.second.(itransfer.CompletionObserver); ok && second != nil {
		second.OnTransferComplete(ctx, source, target, targetEvent)
	}
}

func (c chainedTransferController) OnTransferTerminalError(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
	targetEvent *event.Event,
) {
	if first, ok := c.first.(itransfer.TerminalErrorObserver); ok && first != nil {
		first.OnTransferTerminalError(ctx, source, target, targetEvent)
	}
	if second, ok := c.second.(itransfer.TerminalErrorObserver); ok && second != nil {
		second.OnTransferTerminalError(ctx, source, target, targetEvent)
	}
}

func tighterTimeout(a time.Duration, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}
