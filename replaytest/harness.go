//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Op names for the JSON DSL.
const (
	OpCreateSession           = "create_session"
	OpGetSession              = "get_session"
	OpDeleteSession           = "delete_session"
	OpListSessions            = "list_sessions"
	OpAppendUserEvent         = "append_user_event"
	OpAppendAssistantEvent    = "append_assistant_event"
	OpAppendToolCallEvent     = "append_tool_call_event"
	OpAppendToolResponseEvent = "append_tool_response_event"
	OpUpdateAppState          = "update_app_state"
	OpUpdateUserState         = "update_user_state"
	OpUpdateSessionState      = "update_session_state"
	OpDeleteAppStateKey       = "delete_app_state_key"
	OpDeleteUserStateKey      = "delete_user_state_key"
	OpCreateSummary           = "create_summary"
	OpEnqueueSummary          = "enqueue_summary"
	OpAppendTrackEvent        = "append_track_event"
	OpAddMemory               = "add_memory"
	OpUpdateMemory            = "update_memory"
	OpDeleteMemory            = "delete_memory"
	OpClearMemories           = "clear_memories"
	OpSearchMemories          = "search_memories"
	OpAddMemoryWithMetadata   = "add_memory_with_metadata"
	OpAppendConcurrentEvents  = "append_concurrent_events"
)

// Verify what names.
const (
	VerifySessionFull  = "session_full"
	VerifyEvents       = "events"
	VerifyState        = "state"
	VerifySummary      = "summary"
	VerifyTracks       = "tracks"
	VerifyMemories     = "memories"
	VerifyMemorySearch = "memory_search"
)

// SessionSnapshot captures a backend's view of session data at verification time.
type SessionSnapshot struct {
	Session   *session.Session `json:"session,omitempty"`
	AppState  session.StateMap `json:"app_state,omitempty"`
	UserState session.StateMap `json:"user_state,omitempty"`
}

// MemorySnapshot captures a backend's view of memory data at verification time.
type MemorySnapshot struct {
	Memories      []*memory.Entry `json:"memories,omitempty"`
	SearchResults []*memory.Entry `json:"search_results,omitempty"`
}

// Harness drives operations against a set of backends and captures results.
type Harness struct {
	Spec *Spec

	sessionServices map[string]session.Service
	memoryServices  map[string]memory.Service
	sessionClose    []session.Service
	memoryClose     []memory.Service

	// Active backends after Setup.
	ActiveSessionBackends []string
	ActiveMemoryBackends  []string
	SkippedBackends       map[string]string // name → reason

	// TrackSupport records which session backends implement TrackService.
	TrackSupported map[string]bool

	sessionKey    session.Key
	userKey       session.UserKey
	memoryUserKey memory.UserKey

	dbURL string

	// lastEventIndex tracks the number of events appended to help with event ID mapping during normalization.
	lastEventIndex int
}

// NewHarness creates a new Harness for the given spec.
func NewHarness(spec *Spec, dbURL string) *Harness {
	return &Harness{
		Spec:            spec,
		sessionServices: make(map[string]session.Service),
		memoryServices:  make(map[string]memory.Service),
		sessionKey: session.Key{
			AppName:   spec.Setup.AppName,
			UserID:    spec.Setup.UserID,
			SessionID: spec.Setup.SessionID,
		},
		userKey: session.UserKey{
			AppName: spec.Setup.AppName,
			UserID:  spec.Setup.UserID,
		},
		memoryUserKey: memory.UserKey{
			AppName: spec.Setup.AppName,
			UserID:  spec.Setup.UserID,
		},
		dbURL: dbURL,
	}
}

// Setup creates session/memory services and creates the initial session on all configured backends.
// Before creating new data, it cleans up any leftover data from previous runs to ensure idempotent execution.
func (h *Harness) Setup(ctx context.Context) error {
	h.SkippedBackends = make(map[string]string)
	h.TrackSupported = make(map[string]bool)

	for _, name := range h.Spec.Backends.Session {
		svc, err := NewSessionService(ctx, name, h.dbURL)
		if err != nil {
			h.SkippedBackends[name] = fmt.Sprintf("session: %v", err)
			continue
		}
		h.sessionServices[name] = svc
		h.sessionClose = append(h.sessionClose, svc)
		h.ActiveSessionBackends = append(h.ActiveSessionBackends, name)
		_, h.TrackSupported[name] = svc.(session.TrackService)

		// Clean up leftover data from previous runs (critical for Redis).
		_ = svc.DeleteSession(ctx, h.sessionKey)
		h.cleanAppState(ctx, svc)
		h.cleanUserState(ctx, svc)

		initState := make(session.StateMap)
		for k, v := range h.Spec.Setup.InitState {
			initState[k] = []byte(v)
		}
		if _, err := svc.CreateSession(ctx, h.sessionKey, initState); err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				h.Close()
				return fmt.Errorf("create session on %q: %w", name, err)
			}
		}
	}

	for _, name := range h.Spec.Backends.Memory {
		svc, err := NewMemoryService(ctx, name, h.dbURL)
		if err != nil {
			h.SkippedBackends[name] = fmt.Sprintf("memory: %v", err)
			continue
		}
		h.memoryServices[name] = svc
		h.memoryClose = append(h.memoryClose, svc)
		h.ActiveMemoryBackends = append(h.ActiveMemoryBackends, name)

		// Clean up leftover memories from previous runs.
		_ = svc.ClearMemories(ctx, h.memoryUserKey)
	}

	if len(h.ActiveSessionBackends) == 0 && len(h.ActiveMemoryBackends) == 0 {
		return fmt.Errorf("no backends could be initialized; skipped: %v", h.SkippedBackends)
	}
	return nil
}

// Execute runs all operations in sequence against the appropriate backends.
func (h *Harness) Execute(ctx context.Context) error {
	for i, op := range h.Spec.Operations {
		if err := h.executeOp(ctx, op, i); err != nil {
			return fmt.Errorf("operation %d (%s): %w", i, op.Op, err)
		}
	}
	return nil
}

func (h *Harness) executeOp(ctx context.Context, op Operation, _ int) error {
	switch op.Op {
	case OpCreateSession:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			_, err := svc.CreateSession(ctx, h.sessionKey, nil)
			return err
		})
	case OpGetSession:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			_, err := svc.GetSession(ctx, h.sessionKey)
			return err
		})
	case OpDeleteSession:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return svc.DeleteSession(ctx, h.sessionKey)
		})
	case OpListSessions:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			_, err := svc.ListSessions(ctx, h.userKey)
			return err
		})
	case OpAppendUserEvent:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.appendUserEvent(ctx, svc, op.Params)
		})
	case OpAppendAssistantEvent:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.appendAssistantEvent(ctx, svc, op.Params)
		})
	case OpAppendToolCallEvent:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.appendToolCallEvent(ctx, svc, op.Params)
		})
	case OpAppendToolResponseEvent:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.appendToolResponseEvent(ctx, svc, op.Params)
		})
	case OpUpdateAppState:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.updateAppState(ctx, svc, op.Params)
		})
	case OpUpdateUserState:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.updateUserState(ctx, svc, op.Params)
		})
	case OpUpdateSessionState:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.updateSessionState(ctx, svc, op.Params)
		})
	case OpDeleteAppStateKey:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.deleteAppStateKey(ctx, svc, op.Params)
		})
	case OpDeleteUserStateKey:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.deleteUserStateKey(ctx, svc, op.Params)
		})
	case OpCreateSummary:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.createSummary(ctx, svc, op.Params)
		})
	case OpEnqueueSummary:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.enqueueSummary(ctx, svc, op.Params)
		})
	case OpAppendTrackEvent:
		return h.execSessionOp(ctx, op, func(ctx context.Context, svc session.Service) error {
			return h.appendTrackEvent(ctx, svc, op.Params)
		})
	case OpAddMemory:
		return h.execMemoryOp(ctx, op, func(ctx context.Context, svc memory.Service) error {
			return h.addMemory(ctx, svc, op.Params)
		})
	case OpUpdateMemory:
		return h.execMemoryOp(ctx, op, func(ctx context.Context, svc memory.Service) error {
			return h.updateMemory(ctx, svc, op.Params)
		})
	case OpDeleteMemory:
		return h.execMemoryOp(ctx, op, func(ctx context.Context, svc memory.Service) error {
			return h.deleteMemory(ctx, svc, op.Params)
		})
	case OpClearMemories:
		return h.execMemoryOp(ctx, op, func(ctx context.Context, svc memory.Service) error {
			return svc.ClearMemories(ctx, h.memoryUserKey)
		})
	case OpSearchMemories:
		return h.execMemoryOp(ctx, op, func(ctx context.Context, svc memory.Service) error {
			return h.searchMemories(ctx, svc, op.Params)
		})
	case OpAddMemoryWithMetadata:
		return h.execMemoryOp(ctx, op, func(ctx context.Context, svc memory.Service) error {
			return h.addMemoryWithMetadata(ctx, svc, op.Params)
		})
	case OpAppendConcurrentEvents:
		return h.appendConcurrentEvents(ctx, op.Params)
	default:
		return fmt.Errorf("unknown operation: %q", op.Op)
	}
}

func (h *Harness) execSessionOp(ctx context.Context, op Operation, fn func(context.Context, session.Service) error) error {
	for _, name := range h.ActiveSessionBackends {
		svc := h.sessionServices[name]
		if err := fn(ctx, svc); err != nil {
			if op.Op == OpCreateSession && strings.Contains(err.Error(), "already exists") {
				continue
			}
			return fmt.Errorf("backend %q: %w", name, err)
		}
	}
	h.lastEventIndex++
	return nil
}

func (h *Harness) execMemoryOp(ctx context.Context, _ Operation, fn func(context.Context, memory.Service) error) error {
	for _, name := range h.ActiveMemoryBackends {
		svc := h.memoryServices[name]
		if err := fn(ctx, svc); err != nil {
			return fmt.Errorf("backend %q: %w", name, err)
		}
	}
	return nil
}

// Verify collects snapshots from all backends and returns them for comparison.
func (h *Harness) Verify(ctx context.Context) (map[string]map[string]*SessionSnapshot, map[string]map[string]*MemorySnapshot, error) {
	sessionSnapshots := make(map[string]map[string]*SessionSnapshot)
	memorySnapshots := make(map[string]map[string]*MemorySnapshot)

	for _, name := range h.ActiveSessionBackends {
		snap, err := h.collectSessionSnapshot(ctx, name)
		if err != nil {
			return nil, nil, fmt.Errorf("collect session snapshot %q: %w", name, err)
		}
		sessionSnapshots[name] = snap
	}

	for _, name := range h.ActiveMemoryBackends {
		snap, err := h.collectMemorySnapshot(ctx, name)
		if err != nil {
			return nil, nil, fmt.Errorf("collect memory snapshot %q: %w", name, err)
		}
		memorySnapshots[name] = snap
	}

	return sessionSnapshots, memorySnapshots, nil
}

func (h *Harness) collectSessionSnapshot(ctx context.Context, backendName string) (map[string]*SessionSnapshot, error) {
	svc := h.sessionServices[backendName]
	result := make(map[string]*SessionSnapshot)

	sess, err := svc.GetSession(ctx, h.sessionKey)
	if err != nil {
		return nil, err
	}
	result[VerifySessionFull] = &SessionSnapshot{Session: sess}

	appState, err := svc.ListAppStates(ctx, h.Spec.Setup.AppName)
	if err != nil {
		return nil, fmt.Errorf("list app states: %w", err)
	}
	result[VerifySessionFull].AppState = appState

	userState, err := svc.ListUserStates(ctx, h.userKey)
	if err != nil {
		return nil, fmt.Errorf("list user states: %w", err)
	}
	result[VerifySessionFull].UserState = userState

	return result, nil
}

func (h *Harness) collectMemorySnapshot(ctx context.Context, backendName string) (map[string]*MemorySnapshot, error) {
	svc := h.memoryServices[backendName]
	result := make(map[string]*MemorySnapshot)

	memories, err := svc.ReadMemories(ctx, h.memoryUserKey, 1000)
	if err != nil {
		return nil, fmt.Errorf("read memories: %w", err)
	}
	result[VerifyMemories] = &MemorySnapshot{Memories: memories}

	results, err := svc.SearchMemories(ctx, h.memoryUserKey, "test")
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	result[VerifyMemorySearch] = &MemorySnapshot{SearchResults: results}

	return result, nil
}

// Close cleans up all backend services.
func (h *Harness) Close() error {
	var errs []string
	for _, svc := range h.sessionClose {
		if err := svc.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("session: %v", err))
		}
	}
	for _, svc := range h.memoryClose {
		if err := svc.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("memory: %v", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// --- Event helpers ---

type appendEventArgs struct {
	Author     string                     `json:"author"`
	Content    string                     `json:"content"`
	Branch     string                     `json:"branch,omitempty"`
	Tag        string                     `json:"tag,omitempty"`
	FilterKey  string                     `json:"filterKey,omitempty"`
	StateDelta map[string]string          `json:"state_delta,omitempty"`
	ToolCalls  []toolCallArg              `json:"tool_calls,omitempty"`
	ToolID     string                     `json:"tool_id,omitempty"`
	ToolName   string                     `json:"tool_name,omitempty"`
	ToolCallID string                     `json:"tool_call_id,omitempty"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

type toolCallArg struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

func (h *Harness) appendUserEvent(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args appendEventArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal args: %w", err)
	}
	ev := h.newEvent(args.Author, args.Content, args.Branch, args.Tag, args.FilterKey)
	ev.Response.Choices[0].Message.Role = model.RoleUser
	h.setEventExtensions(ev, args)
	return h.getAndAppend(ctx, svc, ev)
}

func (h *Harness) appendAssistantEvent(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args appendEventArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal args: %w", err)
	}
	ev := h.newEvent(args.Author, args.Content, args.Branch, args.Tag, args.FilterKey)
	ev.Response.Choices[0].Message.Role = model.RoleAssistant
	h.setEventExtensions(ev, args)
	return h.getAndAppend(ctx, svc, ev)
}

func (h *Harness) appendToolCallEvent(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args appendEventArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal args: %w", err)
	}
	ev := h.newEvent(args.Author, "", args.Branch, args.Tag, args.FilterKey)
	ev.Response.Choices[0].Message.Role = model.RoleAssistant
	if len(args.ToolCalls) > 0 {
		toolCalls := make([]model.ToolCall, len(args.ToolCalls))
		for i, tc := range args.ToolCalls {
			toolCalls[i] = model.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      tc.Name,
					Arguments: json.RawMessage(tc.Arguments),
				},
			}
		}
		ev.Response.Choices[0].Message.ToolCalls = toolCalls
	}
	h.setEventExtensions(ev, args)
	return h.getAndAppend(ctx, svc, ev)
}

func (h *Harness) appendToolResponseEvent(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args appendEventArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal args: %w", err)
	}
	ev := h.newEvent(args.Author, args.Content, args.Branch, args.Tag, args.FilterKey)
	ev.Response.Choices[0].Message.Role = model.RoleTool
	ev.Response.Choices[0].Message.ToolID = args.ToolID
	ev.Response.Choices[0].Message.ToolName = args.ToolName
	if args.ToolCallID != "" && args.Extensions == nil {
		tcArgs, _ := json.Marshal(map[string]string{"tool_call_id": args.ToolCallID})
		ev.Extensions = map[string]json.RawMessage{
			event.ToolCallArgsExtensionKey: tcArgs,
		}
	}
	h.setEventExtensions(ev, args)
	return h.getAndAppend(ctx, svc, ev)
}

func (h *Harness) newEvent(author, content, branch, tag, filterKey string) *event.Event {
	now := time.Now().UTC().Truncate(time.Millisecond)
	ev := &event.Event{
		Response: &model.Response{
			ID: fmt.Sprintf("resp-%d", h.lastEventIndex),
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Content: content,
					},
				},
			},
		},
		ID:        fmt.Sprintf("evt-%d-%d", h.lastEventIndex, now.UnixNano()%1000000),
		Author:    author,
		Branch:    branch,
		Tag:       tag,
		FilterKey: filterKey,
		Timestamp: now,
		Version:   event.CurrentVersion,
	}
	if ev.FilterKey == "" {
		ev.FilterKey = branch
	}
	return ev
}

func (h *Harness) setEventExtensions(ev *event.Event, args appendEventArgs) {
	if len(args.StateDelta) > 0 {
		ev.StateDelta = make(map[string][]byte, len(args.StateDelta))
		for k, v := range args.StateDelta {
			ev.StateDelta[k] = []byte(v)
		}
	}
	if len(args.Extensions) > 0 {
		ev.Extensions = make(map[string]json.RawMessage, len(args.Extensions))
		for k, v := range args.Extensions {
			ev.Extensions[k] = v
		}
	}
}

func (h *Harness) getAndAppend(ctx context.Context, svc session.Service, ev *event.Event) error {
	sess, err := svc.GetSession(ctx, h.sessionKey)
	if err != nil {
		return fmt.Errorf("get session for append: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session not found for append")
	}
	return svc.AppendEvent(ctx, sess, ev)
}

// --- Concurrent events ---

// concurrentEventsParams describes a batch of events to be appended concurrently.
type concurrentEventsParams struct {
	Events []appendEventArgs `json:"events"`
}

// appendConcurrentEvents appends multiple events concurrently to all session
// backends. Within each backend, all events are launched in parallel goroutines,
// simulating tool calls or sub-agent events that race against each other.
// After all goroutines complete, the result is verified to contain all events
// regardless of append order.
func (h *Harness) appendConcurrentEvents(ctx context.Context, raw json.RawMessage) error {
	var params concurrentEventsParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return fmt.Errorf("unmarshal concurrent events: %w", err)
	}
	if len(params.Events) == 0 {
		return nil
	}

	// Pre-assign a unique, deterministic index
	startIdx := h.lastEventIndex
	h.lastEventIndex += len(params.Events)

	for _, name := range h.ActiveSessionBackends {
		svc := h.sessionServices[name]

		var wg sync.WaitGroup
		errCh := make(chan error, len(params.Events))

		for i := range params.Events {
			args := params.Events[i]
			idx := startIdx + i
			wg.Add(1)
			go func(a appendEventArgs, eventIdx int) {
				defer wg.Done()
				sess, err := svc.GetSession(ctx, h.sessionKey)
				if err != nil {
					errCh <- fmt.Errorf("concurrent get session on %q: %w", name, err)
					return
				}
				if sess == nil {
					errCh <- fmt.Errorf("concurrent session not found on %q", name)
					return
				}
				ev := h.buildConcurrentEventAt(a, eventIdx)
				if err := svc.AppendEvent(ctx, sess, ev); err != nil {
					errCh <- fmt.Errorf("concurrent append on %q: %w", name, err)
				}
			}(args, idx)
		}

		wg.Wait()
		close(errCh)

		// Collect first error if any.
		for err := range errCh {
			return err
		}
	}
	return nil
}

// buildConcurrentEventAt creates an event using the given stable index so that concurrent goroutines produce deterministic, non-colliding IDs.
func (h *Harness) buildConcurrentEventAt(args appendEventArgs, idx int) *event.Event {
	origIdx := h.lastEventIndex
	h.lastEventIndex = idx
	ev := h.buildConcurrentEvent(args)
	h.lastEventIndex = origIdx
	return ev
}

// buildConcurrentEvent creates an event from args for concurrent appends.
// It determines the event type from the author field or tool data.
func (h *Harness) buildConcurrentEvent(args appendEventArgs) *event.Event {
	var ev *event.Event
	if len(args.ToolCalls) > 0 {
		// Tool call event.
		ev = h.newEvent(args.Author, "", args.Branch, args.Tag, args.FilterKey)
		ev.Response.Choices[0].Message.Role = model.RoleAssistant
		toolCalls := make([]model.ToolCall, len(args.ToolCalls))
		for i, tc := range args.ToolCalls {
			toolCalls[i] = model.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      tc.Name,
					Arguments: json.RawMessage(tc.Arguments),
				},
			}
		}
		ev.Response.Choices[0].Message.ToolCalls = toolCalls
	} else if args.ToolID != "" {
		// Tool response event.
		ev = h.newEvent(args.Author, args.Content, args.Branch, args.Tag, args.FilterKey)
		ev.Response.Choices[0].Message.Role = model.RoleTool
		ev.Response.Choices[0].Message.ToolID = args.ToolID
		ev.Response.Choices[0].Message.ToolName = args.ToolName
		if args.ToolCallID != "" && args.Extensions == nil {
			tcArgs, _ := json.Marshal(map[string]string{"tool_call_id": args.ToolCallID})
			ev.Extensions = map[string]json.RawMessage{
				event.ToolCallArgsExtensionKey: tcArgs,
			}
		}
	} else if args.Author == "user1" || args.Author == "user" {
		ev = h.newEvent(args.Author, args.Content, args.Branch, args.Tag, args.FilterKey)
		ev.Response.Choices[0].Message.Role = model.RoleUser
	} else {
		ev = h.newEvent(args.Author, args.Content, args.Branch, args.Tag, args.FilterKey)
		ev.Response.Choices[0].Message.Role = model.RoleAssistant
	}
	h.setEventExtensions(ev, args)
	return ev
}

// --- State helpers ---

func (h *Harness) updateAppState(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args map[string]string
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal app state: %w", err)
	}
	state := make(session.StateMap, len(args))
	for k, v := range args {
		state[strings.TrimPrefix(k, session.StateAppPrefix)] = []byte(v)
	}
	return svc.UpdateAppState(ctx, h.Spec.Setup.AppName, state)
}

func (h *Harness) updateUserState(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args map[string]string
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal user state: %w", err)
	}
	state := make(session.StateMap, len(args))
	for k, v := range args {
		state[strings.TrimPrefix(k, session.StateUserPrefix)] = []byte(v)
	}
	return svc.UpdateUserState(ctx, h.userKey, state)
}

func (h *Harness) updateSessionState(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args map[string]string
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal session state: %w", err)
	}
	state := make(session.StateMap, len(args))
	for k, v := range args {
		state[k] = []byte(v)
	}
	return svc.UpdateSessionState(ctx, h.sessionKey, state)
}

func (h *Harness) deleteAppStateKey(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal delete app state: %w", err)
	}
	return svc.DeleteAppState(ctx, h.Spec.Setup.AppName, strings.TrimPrefix(args.Key, session.StateAppPrefix))
}

func (h *Harness) deleteUserStateKey(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal delete user state: %w", err)
	}
	return svc.DeleteUserState(ctx, h.userKey, strings.TrimPrefix(args.Key, session.StateUserPrefix))
}

// cleanAppState removes all app-level state for this spec's app.
func (h *Harness) cleanAppState(ctx context.Context, svc session.Service) {
	states, err := svc.ListAppStates(ctx, h.Spec.Setup.AppName)
	if err != nil {
		return
	}
	for k := range states {
		_ = svc.DeleteAppState(ctx, h.Spec.Setup.AppName, k)
	}
}

// cleanUserState removes all user-level state for this spec's user.
func (h *Harness) cleanUserState(ctx context.Context, svc session.Service) {
	states, err := svc.ListUserStates(ctx, h.userKey)
	if err != nil {
		return
	}
	for k := range states {
		_ = svc.DeleteUserState(ctx, h.userKey, k)
	}
}

// --- Summary helpers ---

func (h *Harness) createSummary(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args struct {
		FilterKey string `json:"filterKey,omitempty"`
		Force     bool   `json:"force,omitempty"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal summary args: %w", err)
	}
	sess, err := svc.GetSession(ctx, h.sessionKey)
	if err != nil {
		return fmt.Errorf("get session for summary: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session not found for summary")
	}
	return svc.CreateSessionSummary(ctx, sess, args.FilterKey, args.Force)
}

func (h *Harness) enqueueSummary(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args struct {
		FilterKey string `json:"filterKey,omitempty"`
		Force     bool   `json:"force,omitempty"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal summary args: %w", err)
	}
	sess, err := svc.GetSession(ctx, h.sessionKey)
	if err != nil {
		return fmt.Errorf("get session for enqueue summary: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session not found for enqueue summary")
	}
	return svc.EnqueueSummaryJob(ctx, sess, args.FilterKey, args.Force)
}

// --- Track helpers ---

func (h *Harness) appendTrackEvent(ctx context.Context, svc session.Service, raw json.RawMessage) error {
	var args struct {
		Track   string          `json:"track"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal track event: %w", err)
	}
	sess, err := svc.GetSession(ctx, h.sessionKey)
	if err != nil {
		return fmt.Errorf("get session for track: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session not found for track")
	}

	trackEvent := &session.TrackEvent{
		Track:     session.Track(args.Track),
		Payload:   args.Payload,
		Timestamp: time.Now().UTC().Truncate(time.Millisecond),
	}

	if ts, ok := svc.(session.TrackService); ok {
		return ts.AppendTrackEvent(ctx, sess, trackEvent)
	}
	return sess.AppendTrackEvent(trackEvent)
}

// --- Memory helpers ---

type memoryArgs struct {
	Memory       string   `json:"memory"`
	Topics       []string `json:"topics,omitempty"`
	MemoryID     string   `json:"memory_id,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	EventTime    string   `json:"event_time,omitempty"`
	Participants []string `json:"participants,omitempty"`
	Location     string   `json:"location,omitempty"`
}

func (h *Harness) addMemory(ctx context.Context, svc memory.Service, raw json.RawMessage) error {
	var args memoryArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal memory args: %w", err)
	}
	return svc.AddMemory(ctx, h.memoryUserKey, args.Memory, args.Topics)
}

func (h *Harness) addMemoryWithMetadata(ctx context.Context, svc memory.Service, raw json.RawMessage) error {
	var args memoryArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal memory args: %w", err)
	}
	var opts []memory.AddOption
	if args.Kind != "" || args.EventTime != "" || len(args.Participants) > 0 || args.Location != "" {
		meta := &memory.Metadata{
			Kind:         memory.Kind(args.Kind),
			Participants: args.Participants,
			Location:     args.Location,
		}
		if args.EventTime != "" {
			t, err := time.Parse(time.RFC3339, args.EventTime)
			if err != nil {
				return fmt.Errorf("parse event_time: %w", err)
			}
			meta.EventTime = &t
		}
		opts = append(opts, memory.WithMetadata(meta))
	}
	return svc.AddMemory(ctx, h.memoryUserKey, args.Memory, args.Topics, opts...)
}

func (h *Harness) updateMemory(ctx context.Context, svc memory.Service, raw json.RawMessage) error {
	var args memoryArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal memory args: %w", err)
	}
	memKey := memory.Key{
		AppName:  h.memoryUserKey.AppName,
		UserID:   h.memoryUserKey.UserID,
		MemoryID: args.MemoryID,
	}
	return svc.UpdateMemory(ctx, memKey, args.Memory, args.Topics)
}

func (h *Harness) deleteMemory(ctx context.Context, svc memory.Service, raw json.RawMessage) error {
	var args struct {
		MemoryID string `json:"memory_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal delete memory args: %w", err)
	}
	memKey := memory.Key{
		AppName:  h.memoryUserKey.AppName,
		UserID:   h.memoryUserKey.UserID,
		MemoryID: args.MemoryID,
	}
	return svc.DeleteMemory(ctx, memKey)
}

func (h *Harness) searchMemories(ctx context.Context, svc memory.Service, raw json.RawMessage) error {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return fmt.Errorf("unmarshal search args: %w", err)
	}
	if args.Query == "" {
		args.Query = "test"
	}
	_, err := svc.SearchMemories(ctx, h.memoryUserKey, args.Query)
	return err
}
