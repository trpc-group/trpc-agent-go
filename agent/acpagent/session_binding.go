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
	createdAt  time.Time
	stateDelta session.StateMap
	stateOnce  sync.Once
	configMu   sync.RWMutex
	config     sessionConfiguration
}

type acpSessionStore struct {
	mu      sync.RWMutex
	byKey   map[session.Key]*acpSession
	byID    map[acpsdk.SessionId]*acpSession
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
		byID:  make(map[acpsdk.SessionId]*acpSession),
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
	if ctx == nil {
		ctx = context.Background()
	}
	if invocation.Session == nil {
		err := errors.New("acpagent: invocation session is nil")
		warnContext(ctx, "acpagent: invalid invocation session", "error", err)
		return nil, err
	}
	if invocation.SessionService == nil {
		warnContext(
			ctx,
			"acpagent: session service is required",
			append(invocationLogFields(invocation), "error", errSessionServiceRequired)...,
		)
		return nil, errSessionServiceRequired
	}
	key := session.Key{
		AppName:   invocation.Session.AppName,
		UserID:    invocation.Session.UserID,
		SessionID: invocation.Session.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		err = fmt.Errorf("acpagent: invalid session key: %w", err)
		warnContext(ctx, "acpagent: invalid session key", append(sessionLogFields(key), "error", err)...)
		return nil, err
	}
	releaseActivity := a.connections.beginActivity()
	defer releaseActivity()
	process, err := a.connections.connect(ctx)
	if err != nil {
		return nil, err
	}
	fields := append(sessionLogFields(key), processLogFields(process)...)
	fields = append(fields, "agent", a.config.Name)
	debugContext(ctx, "acpagent: acquiring ACP session", fields...)
	flightKey := key.AppName + "\x00" + key.UserID + "\x00" + key.SessionID
	result := a.sessions.acquire.DoChan(flightKey, func() (any, error) {
		return a.acquireSessionOnce(ctx, invocation, process, key)
	})
	select {
	case <-ctx.Done():
		debugContext(ctx, "acpagent: session acquisition canceled", append(fields, "error", ctx.Err())...)
		return nil, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			errorContext(ctx, "acpagent: session acquisition failed", append(fields, "error", result.Err)...)
			return nil, result.Err
		}
		binding, ok := result.Val.(*acpSession)
		if !ok {
			err := errors.New("acpagent: invalid session acquisition result")
			errorContext(ctx, "acpagent: invalid session acquisition result", append(fields, "error", err)...)
			return nil, err
		}
		return binding, nil
	}
}

// CloseSession closes the ACP session mapped to key.
//
// CloseSession is idempotent: closing an unmapped session returns nil. Persisted
// ACP session state is retained; a later acquire can recover it or create a new
// session after an Unknown Session response.
func (a *ACPAgent) CloseSession(ctx context.Context, key session.Key) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := key.CheckSessionKey(); err != nil {
		err = fmt.Errorf("acpagent: invalid session key: %w", err)
		warnContext(ctx, "acpagent: invalid session key", append(sessionLogFields(key), "error", err)...)
		return err
	}
	releaseActivity := a.connections.beginActivity()
	defer releaseActivity()
	debugContext(ctx, "acpagent: closing ACP session", sessionLogFields(key)...)

	binding := a.sessions.lookup(key)
	if binding == nil {
		debugContext(ctx, "acpagent: no ACP session binding to close", sessionLogFields(key)...)
		return nil
	}
	if err := binding.acquirePrompt(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			debugContext(ctx, "acpagent: close ACP session canceled while waiting for prompt", acpSessionLogFields(binding)...)
		} else {
			warnContext(ctx, "acpagent: close ACP session failed to acquire prompt gate", append(acpSessionLogFields(binding), "error", err)...)
		}
		return err
	}
	defer binding.releasePrompt()
	if err := closeACPSession(ctx, binding.process, binding.id); err != nil {
		if errors.Is(err, errSessionCloseUnsupported) {
			debugContext(ctx, "acpagent: session close is not supported", acpSessionLogFields(binding)...)
		} else {
			warnContext(ctx, "acpagent: close ACP session failed", append(acpSessionLogFields(binding), "error", err)...)
		}
		return err
	}
	a.sessions.remove(key, binding)
	infoContext(ctx, "acpagent: closed ACP session", acpSessionLogFields(binding)...)
	return nil
}

func (a *ACPAgent) acquireSessionOnce(
	ctx context.Context,
	invocation *agent.Invocation,
	process *processConnection,
	key session.Key,
) (*acpSession, error) {
	createdAt := invocation.Session.CreatedAt
	existing := a.sessions.lookup(key)
	sessionIDHint := acpsdk.SessionId("")
	if existing != nil {
		switch {
		case !sameACPSession(existing, invocation.Session):
			infoContext(
				ctx,
				"acpagent: replacing ACP session binding",
				append(acpSessionLogFields(existing), "agent", a.config.Name)...,
			)
			if err := a.closeReplacedSession(process, existing); err != nil {
				return nil, err
			}
			a.sessions.remove(key, existing)
			if err := a.clearPersistedSessionID(ctx, invocation); err != nil {
				return nil, err
			}
		case existing.generation == process.generation &&
			existing.process == process:
			existing.applySessionState(invocation.Session)
			debugContext(
				ctx,
				"acpagent: reusing ACP session binding",
				append(acpSessionLogFields(existing), "agent", a.config.Name)...,
			)
			return existing, nil
		default:
			sessionIDHint = existing.id
			a.sessions.remove(key, existing)
			debugContext(
				ctx,
				"acpagent: rebinding ACP session to a new process",
				append(
					acpSessionLogFields(existing),
					"agent", a.config.Name,
					"new_generation", process.generation,
				)...,
			)
		}
	}

	sessionID := sessionIDHint
	if sessionID == "" {
		sessionID = a.persistedSessionID(invocation.Session)
	}
	if sessionID != "" {
		debugContext(
			ctx,
			"acpagent: restoring ACP session",
			append(
				sessionLogFields(key),
				"agent", a.config.Name,
				"acp_session_id", string(sessionID),
			)...,
		)
		supported, config, err := a.restoreACPSession(ctx, process, sessionID)
		if err != nil {
			if isUnknownSessionError(err) {
				warnContext(
					ctx,
					"acpagent: persisted ACP session is unknown; clearing session state",
					append(
						sessionLogFields(key),
						"agent", a.config.Name,
						"acp_session_id", string(sessionID),
						"error", err,
					)...,
				)
				if clearErr := a.clearPersistedSessionID(ctx, invocation); clearErr != nil {
					return nil, errors.Join(err, clearErr)
				}
			} else {
				return nil, err
			}
		} else if supported {
			binding := a.newSessionBinding(
				key,
				sessionID,
				process,
				createdAt,
				nil,
				config,
			)
			a.sessions.put(binding)
			return binding, nil
		}
		debugContext(
			ctx,
			"acpagent: ACP session cannot be restored; creating a new session",
			append(sessionLogFields(key), "agent", a.config.Name)...,
		)
	}

	var response acpsdk.NewSessionResponse
	debugContext(
		ctx,
		"acpagent: creating ACP session",
		append(
			sessionLogFields(key),
			processLogFields(process)...,
		)...,
	)
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
		closeErr := closeACPSessionWithTimeout(process, response.SessionId)
		errorContext(
			ctx,
			"acpagent: persist ACP session state failed",
			append(
				sessionLogFields(key),
				"agent", a.config.Name,
				"acp_session_id", string(response.SessionId),
				"error", errors.Join(persistErr, closeErr),
			)...,
		)
		return nil, errors.Join(persistErr, closeErr)
	}
	for stateKey, value := range state {
		invocation.Session.SetState(stateKey, value)
	}
	binding := a.newSessionBinding(
		key,
		response.SessionId,
		process,
		createdAt,
		cloneSessionState(state),
		newSessionConfiguration(response.ConfigOptions),
	)
	a.sessions.put(binding)
	infoContext(
		ctx,
		"acpagent: created ACP session",
		append(acpSessionLogFields(binding), "agent", a.config.Name)...,
	)
	return binding, nil
}

func (a *ACPAgent) restoreACPSession(
	ctx context.Context,
	process *processConnection,
	sessionID acpsdk.SessionId,
) (bool, sessionConfiguration, error) {
	if process.agentDescriptor.agentCapabilities.SessionCapabilities.Resume != nil {
		debugContext(
			ctx,
			"acpagent: resuming ACP session",
			append(
				processLogFields(process),
				"agent", a.config.Name,
				"acp_session_id", string(sessionID),
			)...,
		)
		var config sessionConfiguration
		err := a.connections.withSessionAuthentication(
			ctx,
			process,
			sessionAcquisitionResume,
			func(callCtx context.Context) error {
				response, err := process.connection.ResumeSession(
					callCtx,
					acpsdk.ResumeSessionRequest{
						SessionId:  sessionID,
						Cwd:        a.config.WorkDir,
						McpServers: []acpsdk.McpServer{},
					},
				)
				if err == nil {
					config = newSessionConfiguration(response.ConfigOptions)
					infoContext(
						ctx,
						"acpagent: resumed ACP session",
						append(
							processLogFields(process),
							"agent", a.config.Name,
							"acp_session_id", string(sessionID),
						)...,
					)
				}
				return err
			},
		)
		return true, config, err
	}
	if process.agentDescriptor.agentCapabilities.LoadSession {
		debugContext(
			ctx,
			"acpagent: loading ACP session",
			append(
				processLogFields(process),
				"agent", a.config.Name,
				"acp_session_id", string(sessionID),
			)...,
		)
		var config sessionConfiguration
		err := a.connections.withSessionAuthentication(
			ctx,
			process,
			sessionAcquisitionLoad,
			func(callCtx context.Context) error {
				response, err := process.connection.LoadSession(
					callCtx,
					acpsdk.LoadSessionRequest{
						SessionId:  sessionID,
						Cwd:        a.config.WorkDir,
						McpServers: []acpsdk.McpServer{},
					},
				)
				if err == nil {
					config = newSessionConfiguration(response.ConfigOptions)
					infoContext(
						ctx,
						"acpagent: loaded ACP session",
						append(
							processLogFields(process),
							"agent", a.config.Name,
							"acp_session_id", string(sessionID),
						)...,
					)
				}
				return err
			},
		)
		return true, config, err
	}
	return false, sessionConfiguration{}, nil
}

func (a *ACPAgent) persistedSessionID(target *session.Session) acpsdk.SessionId {
	if target == nil {
		return ""
	}
	keys := newACPSessionStateKeys(a.config.Name)
	rawID, ok := target.GetState(keys.sessionID)
	if !ok || len(rawID) == 0 {
		return ""
	}
	rawFingerprint, ok := target.GetState(keys.launchFingerprint)
	if !ok || string(rawFingerprint) != a.launchFingerprint {
		return ""
	}
	return acpsdk.SessionId(rawID)
}

func (a *ACPAgent) clearPersistedSessionID(
	ctx context.Context,
	invocation *agent.Invocation,
) error {
	key := session.Key{
		AppName:   invocation.Session.AppName,
		UserID:    invocation.Session.UserID,
		SessionID: invocation.Session.ID,
	}
	stateKey := newACPSessionStateKeys(a.config.Name).sessionID
	if err := invocation.SessionService.UpdateSessionState(ctx, key, session.StateMap{
		stateKey: nil,
	}); err != nil {
		return fmt.Errorf("acpagent: clear stale ACP session ID: %w", err)
	}
	invocation.Session.SetState(stateKey, nil)
	debugContext(
		ctx,
		"acpagent: cleared persisted ACP session id",
		append(
			sessionLogFields(key),
			"agent", a.config.Name,
			"state_key", stateKey,
		)...,
	)
	return nil
}

func (a *ACPAgent) closeReplacedSession(
	current *processConnection,
	old *acpSession,
) error {
	if old == nil || old.process == nil || old.process != current {
		return nil
	}
	err := closeACPSessionWithTimeout(old.process, old.id)
	if errors.Is(err, errSessionCloseUnsupported) {
		return nil
	}
	return err
}

func (a *ACPAgent) newSessionBinding(
	key session.Key,
	sessionID acpsdk.SessionId,
	process *processConnection,
	createdAt time.Time,
	stateDelta session.StateMap,
	config sessionConfiguration,
) *acpSession {
	binding := &acpSession{
		key:        key,
		id:         sessionID,
		generation: process.generation,
		process:    process,
		promptGate: make(chan struct{}, 1),
		createdAt:  createdAt,
		stateDelta: stateDelta,
		config:     config.clone(),
	}
	binding.promptGate <- struct{}{}
	return binding
}

func sameACPSession(binding *acpSession, target *session.Session) bool {
	if binding == nil || target == nil {
		return false
	}
	if binding.createdAt.IsZero() || target.CreatedAt.IsZero() {
		return binding.createdAt.IsZero() && target.CreatedAt.IsZero()
	}
	return binding.createdAt.Equal(target.CreatedAt)
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

func (s *acpSessionStore) lookup(key session.Key) *acpSession {
	s.mu.RLock()
	binding := s.byKey[key]
	s.mu.RUnlock()
	return binding
}

func (s *acpSessionStore) lookupByID(sessionID acpsdk.SessionId) *acpSession {
	s.mu.RLock()
	binding := s.byID[sessionID]
	s.mu.RUnlock()
	return binding
}

func (s *acpSessionStore) remove(key session.Key, binding *acpSession) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.byKey[key] == binding {
		delete(s.byKey, key)
	}
	if binding != nil && s.byID[binding.id] == binding {
		delete(s.byID, binding.id)
	}
	s.mu.Unlock()
}

func (s *acpSessionStore) put(binding *acpSession) {
	if binding == nil {
		return
	}
	s.mu.Lock()
	if existing := s.byKey[binding.key]; existing != nil &&
		existing != binding &&
		s.byID[existing.id] == existing {
		delete(s.byID, existing.id)
	}
	s.byKey[binding.key] = binding
	s.byID[binding.id] = binding
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

func closeACPSessionWithTimeout(
	process *processConnection,
	sessionID acpsdk.SessionId,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultSessionCloseTimeout)
	defer cancel()
	return closeACPSession(ctx, process, sessionID)
}

func closeACPSession(
	ctx context.Context,
	process *processConnection,
	sessionID acpsdk.SessionId,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if process == nil || process.connection == nil {
		err := errors.New("acpagent: ACP process is unavailable for session cleanup")
		warnContext(
			ctx,
			"acpagent: ACP process is unavailable for session cleanup",
			"acp_session_id", string(sessionID),
			"error", err,
		)
		return err
	}
	if process.agentDescriptor.agentCapabilities.SessionCapabilities.Close == nil {
		debugContext(
			ctx,
			"acpagent: session close capability is not advertised",
			append(
				processLogFields(process),
				"acp_session_id", string(sessionID),
			)...,
		)
		return errSessionCloseUnsupported
	}
	_, err := process.connection.CloseSession(ctx, acpsdk.CloseSessionRequest{
		SessionId: sessionID,
	})
	if isUnknownSessionError(err) {
		debugContext(
			ctx,
			"acpagent: ACP session is already unknown during close",
			append(
				processLogFields(process),
				"acp_session_id", string(sessionID),
			)...,
		)
		return nil
	}
	if err != nil {
		return fmt.Errorf("acpagent: close ACP session: %w", err)
	}
	return nil
}

func isUnknownSessionError(err error) bool {
	var requestErr *acpsdk.RequestError
	return errors.As(err, &requestErr) && requestErr.Code == -32002
}

func computeLaunchFingerprint(config processConfig) (string, error) {
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
