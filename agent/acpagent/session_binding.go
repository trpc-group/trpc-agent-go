//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"golang.org/x/sync/singleflight"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const defaultSessionCloseTimeout = 5 * time.Second

var (
	errSessionCloseUnsupported = errors.New("acpagent: session close capability is not advertised")
	errSessionServiceRequired  = errors.New("acpagent: session service is required")
)

type acpSession struct {
	key        session.Key
	id         acpsdk.SessionId
	generation uint64
	process    *processConnection
	promptGate chan struct{}
	stateDelta session.StateMap
	stateOnce  sync.Once
}

type acpSessionStore struct {
	mu      sync.RWMutex
	byKey   map[session.Key]*acpSession
	acquire singleflight.Group
}

type acpSessionStateKeys struct {
	sessionID         string
	launchFingerprint string
	agentVersion      string
}

func newACPSessionStore() *acpSessionStore {
	return &acpSessionStore{
		byKey: make(map[session.Key]*acpSession),
	}
}

func newACPSessionStateKeys(agentName string) acpSessionStateKeys {
	prefix := "agent.acp." + agentName + "."
	return acpSessionStateKeys{
		sessionID:         prefix + "session_id",
		launchFingerprint: prefix + "launch_fingerprint",
		agentVersion:      prefix + "agent_version",
	}
}

func (a *ACPAgent) acquireSession(
	ctx context.Context,
	invocation *agent.Invocation,
) (*acpSession, error) {
	if invocation == nil {
		return nil, errors.New("acpagent: invocation is nil")
	}
	if invocation.Session == nil {
		return nil, errors.New("acpagent: invocation session is nil")
	}
	if invocation.SessionService == nil {
		return nil, errSessionServiceRequired
	}
	key := session.Key{
		AppName:   invocation.Session.AppName,
		UserID:    invocation.Session.UserID,
		SessionID: invocation.Session.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return nil, fmt.Errorf("acpagent: invalid session key: %w", err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	process, err := a.connections.connect(ctx)
	if err != nil {
		return nil, err
	}
	flightKey := key.AppName + "\x00" + key.UserID + "\x00" + key.SessionID
	result := a.sessions.acquire.DoChan(flightKey, func() (any, error) {
		return a.acquireSessionOnce(ctx, invocation, process, key)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return nil, result.Err
		}
		binding, ok := result.Val.(*acpSession)
		if !ok {
			return nil, errors.New("acpagent: invalid session acquisition result")
		}
		return binding, nil
	}
}

func (a *ACPAgent) acquireSessionOnce(
	ctx context.Context,
	invocation *agent.Invocation,
	process *processConnection,
	key session.Key,
) (*acpSession, error) {
	if binding := a.sessions.get(key, process.generation); binding != nil {
		binding.applySessionState(invocation.Session)
		return binding, nil
	}

	var response acpsdk.NewSessionResponse
	err := a.connections.withSessionAuthentication(
		ctx,
		process,
		sessionAcquisitionNew,
		func(callCtx context.Context) error {
			var callErr error
			response, callErr = process.connection.NewSession(callCtx, acpsdk.NewSessionRequest{
				Cwd:        a.config.WorkDir,
				McpServers: []acpsdk.McpServer{},
			})
			return callErr
		},
	)
	if err != nil {
		return nil, err
	}

	state := a.sessionState(response.SessionId, process)
	if err := invocation.SessionService.UpdateSessionState(ctx, key, state); err != nil {
		persistErr := fmt.Errorf("acpagent: persist ACP session state: %w", err)
		closeErr := closeRejectedACPSession(process, response.SessionId)
		return nil, errors.Join(persistErr, closeErr)
	}
	for stateKey, value := range state {
		invocation.Session.SetState(stateKey, value)
	}
	binding := &acpSession{
		key:        key,
		id:         response.SessionId,
		generation: process.generation,
		process:    process,
		promptGate: make(chan struct{}, 1),
		stateDelta: cloneSessionState(state),
	}
	binding.promptGate <- struct{}{}
	a.sessions.put(binding)
	return binding, nil
}

func (a *ACPAgent) sessionState(
	sessionID acpsdk.SessionId,
	process *processConnection,
) session.StateMap {
	keys := newACPSessionStateKeys(a.config.Name)
	agentVersion := ""
	if process.agentDescriptor.agentInfo != nil {
		agentVersion = process.agentDescriptor.agentInfo.Version
	}
	return session.StateMap{
		keys.sessionID:         []byte(sessionID),
		keys.launchFingerprint: []byte(a.launchFingerprint),
		keys.agentVersion:      []byte(agentVersion),
	}
}

func (s *acpSessionStore) get(
	key session.Key,
	generation uint64,
) *acpSession {
	s.mu.RLock()
	binding := s.byKey[key]
	s.mu.RUnlock()
	if binding == nil || binding.generation != generation {
		return nil
	}
	return binding
}

func (s *acpSessionStore) put(binding *acpSession) {
	if binding == nil {
		return
	}
	s.mu.Lock()
	s.byKey[binding.key] = binding
	s.mu.Unlock()
}

func (s *acpSession) applySessionState(target *session.Session) {
	if s == nil || target == nil {
		return
	}
	for key, value := range s.stateDelta {
		target.SetState(key, value)
	}
}

func (s *acpSession) applyStateDelta(target *event.Event) {
	if s == nil || target == nil {
		return
	}
	s.stateOnce.Do(func() {
		if target.StateDelta == nil {
			target.StateDelta = make(session.StateMap, len(s.stateDelta))
		}
		for key, value := range s.stateDelta {
			target.StateDelta[key] = append([]byte(nil), value...)
		}
	})
}

func (s *acpSession) acquirePrompt(ctx context.Context) error {
	if s == nil || s.promptGate == nil {
		return errors.New("acpagent: ACP session prompt gate is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.promptGate:
		return nil
	}
}

func (s *acpSession) releasePrompt() {
	if s == nil || s.promptGate == nil {
		return
	}
	s.promptGate <- struct{}{}
}

func closeRejectedACPSession(
	process *processConnection,
	sessionID acpsdk.SessionId,
) error {
	if process == nil || process.connection == nil {
		return errors.New("acpagent: ACP process is unavailable for session cleanup")
	}
	if process.agentDescriptor.agentCapabilities.SessionCapabilities.Close == nil {
		return errSessionCloseUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultSessionCloseTimeout)
	defer cancel()
	_, err := process.connection.CloseSession(ctx, acpsdk.CloseSessionRequest{
		SessionId: sessionID,
	})
	if isUnknownSessionError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("acpagent: close rejected ACP session: %w", err)
	}
	return nil
}

func isUnknownSessionError(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32002
}

func computeLaunchFingerprint(config ACPConfig) (string, error) {
	envKeys := make([]string, 0, len(config.Env))
	for _, entry := range config.Env {
		key, _, _ := strings.Cut(entry, "=")
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)
	payload, err := json.Marshal(struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
		WorkDir string   `json:"work_dir"`
		EnvKeys []string `json:"env_keys"`
	}{
		Command: config.Command,
		Args:    config.Args,
		WorkDir: config.WorkDir,
		EnvKeys: envKeys,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func cloneSessionState(state session.StateMap) session.StateMap {
	cloned := make(session.StateMap, len(state))
	for key, value := range state {
		cloned[key] = append([]byte(nil), value...)
	}
	return cloned
}
