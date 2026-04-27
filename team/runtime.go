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
	"trpc.group/trpc-go/trpc-agent-go/internal/eventcontrol"
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
	sessions     map[string]swarmInvocationSession
	branches     map[string]swarmInvocationSession
}

type swarmInvocationSession struct {
	session *session.Session
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
	msg, err := sr.inputBuilder(ctx, SwarmHandoffInputArgs{
		FromAgentName:   sourceAgentName(source),
		ToAgentName:     target.AgentName,
		RootInput:       rootMessage(source),
		ParentInput:     sourceMessage(source),
		TransferMessage: target.Message.Content,
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
	sess, err := sr.sessionForTransferTarget(ctx, source, target, root)
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
		return sr.perAgentSession(ctx, service, root, "", agentName)
	}
}

func (sr *swarmRuntime) sessionForTransferTarget(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
	root *session.Session,
) (*session.Session, error) {
	switch sr.handoff.normalizedSessionScope() {
	case swarmSessionScopeShared:
		return target.Session, nil
	default:
		return sr.perAgentSession(ctx, target.SessionService, root, sourceAgentName(source), target.AgentName)
	}
}

func (sr *swarmRuntime) perAgentSession(
	ctx context.Context,
	service session.Service,
	root *session.Session,
	fromAgent string,
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
		if err := sr.setMemberSessionState(ctx, service, root, toAgent, root.ID); err != nil {
			return nil, err
		}
		return root, nil
	}
	if sessionID, ok := root.GetState(swarmMemberSessionKey(sr.teamName, toAgent)); ok && len(sessionID) > 0 {
		return sr.getOrCreateSession(ctx, service, root, string(sessionID))
	}
	sess, err := sr.newIsolatedSession(ctx, service, root, fromAgent, toAgent)
	if err != nil {
		return nil, err
	}
	if err := sr.setMemberSessionState(ctx, service, root, toAgent, sess.ID); err != nil {
		return nil, err
	}
	return sess, nil
}

func (sr *swarmRuntime) newIsolatedSession(
	ctx context.Context,
	service session.Service,
	root *session.Session,
	fromAgent string,
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
	sessionID := sr.isolatedSessionID(root, fromAgent, toAgent)
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
		return nil, fmt.Errorf("create isolated session: %w", err)
	}
	return sess, nil
}

func (sr *swarmRuntime) setMemberSessionState(
	ctx context.Context,
	service session.Service,
	root *session.Session,
	agentName string,
	sessionID string,
) error {
	if root == nil || agentName == "" || sessionID == "" {
		return nil
	}
	key := swarmMemberSessionKey(sr.teamName, agentName)
	value := []byte(sessionID)
	root.SetState(key, value)
	if service == nil {
		return nil
	}
	err := service.UpdateSessionState(ctx, session.Key{
		AppName:   root.AppName,
		UserID:    root.UserID,
		SessionID: root.ID,
	}, session.StateMap{key: value})
	if err != nil {
		return fmt.Errorf("update swarm member session state: %w", err)
	}
	return nil
}

func (sr *swarmRuntime) isolatedSessionID(
	root *session.Session,
	fromAgent string,
	toAgent string,
) string {
	return defaultSwarmSessionID(swarmSessionIDArgs{
		ParentSessionID: root.ID,
		TeamName:        sr.teamName,
		EntryAgentName:  sr.entryName,
		FromAgentName:   fromAgent,
		ToAgentName:     toAgent,
	})
}

func (sr *swarmRuntime) OnTransferComplete(
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
	if sameSession(root, target.Session) {
		if targetEvent.StateDelta == nil {
			targetEvent.StateDelta = make(map[string][]byte)
		}
		targetEvent.StateDelta[activeAgentKey] = activeAgentValue
	}
	if source == nil || source.SessionService == nil {
		return
	}
	key := session.Key{
		AppName:   root.AppName,
		UserID:    root.UserID,
		SessionID: root.ID,
	}
	if err := source.SessionService.UpdateSessionState(ctx, key, state); err != nil {
		log.DebugfContext(ctx, "save active swarm agent state skipped or failed: %v", err)
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
	route := swarmInvocationSession{session: sess}
	if invocationID == "" && branch == "" {
		return
	}
	if sr.sessions == nil {
		sr.sessions = make(map[string]swarmInvocationSession)
	}
	if invocationID != "" {
		sr.sessions[invocationID] = route
	}
	if branch == "" {
		return
	}
	if sr.branches == nil {
		sr.branches = make(map[string]swarmInvocationSession)
	}
	sr.branches[branch] = route
}

func (sr *swarmRuntime) sessionForEvent(
	evt *event.Event,
) (swarmInvocationSession, bool) {
	if sr == nil || evt == nil {
		return swarmInvocationSession{}, false
	}
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if len(sr.sessions) == 0 && len(sr.branches) == 0 {
		return swarmInvocationSession{}, false
	}
	if route, ok := sr.sessions[evt.InvocationID]; ok && route.session != nil {
		return route, true
	}
	if route, ok := sr.sessions[evt.ParentInvocationID]; ok && route.session != nil {
		if evt.InvocationID != "" {
			sr.sessions[evt.InvocationID] = route
		}
		return route, true
	}
	if route, ok := sr.sessionForBranchLocked(evt.Branch); ok && route.session != nil {
		if evt.InvocationID != "" {
			sr.sessions[evt.InvocationID] = route
		}
		return route, true
	}
	return swarmInvocationSession{}, false
}

func (sr *swarmRuntime) sessionForBranchLocked(
	branch string,
) (swarmInvocationSession, bool) {
	var (
		bestRoute swarmInvocationSession
		bestLen   int
	)
	for prefix, route := range sr.branches {
		if prefix == "" || !branchMatchesPrefix(branch, prefix) {
			continue
		}
		if len(prefix) > bestLen {
			bestRoute = route
			bestLen = len(prefix)
		}
	}
	return bestRoute, bestLen > 0
}

func branchMatchesPrefix(branch string, prefix string) bool {
	return branch == prefix || strings.HasPrefix(branch, prefix+agent.BranchDelimiter)
}

// HandleEventPersistence persists isolated member events after runner plugins run.
func (sr *swarmRuntime) HandleEventPersistence(
	ctx context.Context,
	root *agent.Invocation,
	routeEvt *event.Event,
	evt *event.Event,
) bool {
	if sr == nil || !sr.handoff.usesIsolatedSession() || root == nil || routeEvt == nil || evt == nil {
		return false
	}
	route, ok := sr.sessionForEvent(routeEvt)
	if !ok || sameSession(root.Session, route.session) {
		return false
	}
	if root.SessionService == nil || !swarmShouldPersistEvent(evt) {
		markSkipPersistence(root, evt)
		return true
	}
	if err := root.SessionService.AppendEvent(ctx, route.session, evt); err != nil {
		log.DebugfContext(ctx, "append isolated swarm event skipped or failed: %v", err)
		markSkipPersistence(root, evt)
		return true
	}
	if shouldSummarizeSwarmEvent(evt) {
		if err := root.SessionService.EnqueueSummaryJob(ctx, route.session, evt.FilterKey, false); err != nil {
			log.DebugfContext(ctx, "summarize isolated swarm event skipped or failed: %v", err)
		}
	}
	markSkipPersistence(root, evt)
	return true
}

func markSkipPersistence(root *agent.Invocation, evt *event.Event) {
	eventcontrol.MarkSkipPersistence(root, evt)
}

func swarmShouldPersistEvent(evt *event.Event) bool {
	if evt == nil {
		return false
	}
	return len(evt.StateDelta) > 0 ||
		(evt.Response != nil && !evt.IsPartial && evt.IsValidContent())
}

func shouldSummarizeSwarmEvent(evt *event.Event) bool {
	if evt == nil || evt.IsUserMessage() ||
		evt.IsToolCallResponse() || !evt.IsValidContent() {
		return false
	}
	if evt.Actions != nil && evt.Actions.SkipSummarization {
		return false
	}
	return true
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

func swarmMemberSessionKey(teamName string, agentName string) string {
	if teamName == "" {
		return "swarm_agent_session:" + agentName
	}
	return "swarm_agent_session:" + teamName + ":" + agentName
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
	if first, ok := c.first.(transferInvocationCustomizer); ok && first != nil {
		if err := first.CustomizeTransferInvocation(ctx, source, target); err != nil {
			return err
		}
	}
	if second, ok := c.second.(transferInvocationCustomizer); ok && second != nil {
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
	if first, ok := c.first.(transferCompletionObserver); ok && first != nil {
		first.OnTransferComplete(ctx, source, target, targetEvent)
	}
	if second, ok := c.second.(transferCompletionObserver); ok && second != nil {
		second.OnTransferComplete(ctx, source, target, targetEvent)
	}
}

type transferInvocationCustomizer interface {
	CustomizeTransferInvocation(
		ctx context.Context,
		source *agent.Invocation,
		target *agent.Invocation,
	) error
}

type transferCompletionObserver interface {
	OnTransferComplete(
		ctx context.Context,
		source *agent.Invocation,
		target *agent.Invocation,
		targetEvent *event.Event,
	)
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
