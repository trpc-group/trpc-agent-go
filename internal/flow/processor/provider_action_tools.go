//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Only binding identities and their lifetime cross the persistence boundary. The
// tool objects themselves stay in the invocation that performed the search.
const actionToolsExtension = "trpc.provider_action.tool_bindings.v1"

type actionToolState struct {
	producer      model.Info
	live          map[string]map[string]tool.Tool // result call ID -> encoded identity -> tool
	saved         map[string]json.RawMessage
	response      *model.Response
	justCompleted []model.Message // results emitted after the current mixed response's request
}

func actionToolsForInvocation(inv *agent.Invocation) *actionToolState {
	return actionToolsForProducer(inv, inv.Model.Info())
}

func actionToolsForProducer(inv *agent.Invocation, info model.Info) *actionToolState {
	key := fmt.Sprintf("model:action-tools:%q:%q", info.ProviderDataProtocol, info.Name)
	if state, ok := agent.GetStateValue[*actionToolState](inv, key); ok {
		return state
	}
	state := &actionToolState{producer: info, live: make(map[string]map[string]tool.Tool)}
	inv.SetState(key, state)
	return state
}

// Each result belongs to the model that produced it. Model switches can put
// several producers in a request, and call IDs are local to their history.
func actionResultStates(inv *agent.Invocation, req *model.Request) map[string]*actionToolState {
	states := make(map[string]*actionToolState)
	if req == nil {
		return states
	}
	protocol := inv.Model.Info().ProviderDataProtocol
	for _, msg := range req.Messages {
		data := msg.ProviderData
		if msg.Role == model.RoleTool && data != nil && data.Protocol == protocol {
			states[msg.ToolID] = actionToolsForProducer(inv, model.Info{Name: data.Model, ProviderDataProtocol: protocol})
		}
	}
	return states
}

// collect stages the bindings for one action batch. A nil value denotes the
// registered tool, which should follow the next request's active directory.
func (s *actionToolState) collect(ctx context.Context, inv *agent.Invocation, req *model.Request, savedResults []model.Message) (
	context.Context, map[string]map[string]tool.Tool, error,
) {
	saved := make(map[string]map[string]bool)
	origins := actionResultStates(inv, req)
	for id, origin := range origins {
		bindings, err := origin.persistedBindings(inv, id)
		if err != nil {
			return nil, nil, err
		}
		saved[id] = bindings
	}
	staged := make(map[string]map[string]tool.Tool)
	actions := &providerdata.ActionTools{
		SavedResults: savedResults,
		Expired: func(id string, identity providerdata.ActionToolIdentity) bool {
			origin := origins[id]
			if origin == nil {
				origin = s
			}
			_, missing := resolveActionTool(origin.live[id], saved[id], identity)
			return missing
		},
		Bind: func(id, name string, identity providerdata.ActionToolIdentity, t tool.Tool) bool {
			// Compare the interface as offered and returned, including concrete
			// types that cannot be compared with ==. Selection of an original
			// entry needs neither another filter call nor a run-only executor.
			if req != nil && reflect.ValueOf(req.Tools[name]) == reflect.ValueOf(t) {
				t = nil
			} else if filter := inv.RunOptions.ToolFilter; filter != nil && !filter(ctx, itool.ResolveDeclaration(t)) {
				return false
			}
			if staged[id] == nil {
				staged[id] = make(map[string]tool.Tool)
			}
			staged[id][actionIdentityKey(identity)] = t
			return true
		},
	}
	return providerdata.WithActionTools(ctx, actions), staged, nil
}

func recordActionToolKeys(evt *event.Event, bindings map[string]tool.Tool) {
	if len(bindings) == 0 {
		return
	}
	runOnly := make(map[string]bool, len(bindings))
	for identity, t := range bindings {
		runOnly[identity] = t != nil
	}
	encoded, _ := json.Marshal(runOnly) // a map of string keys to bool
	if evt.Extensions == nil {
		evt.Extensions = make(map[string]json.RawMessage)
	}
	evt.Extensions[actionToolsExtension] = encoded
}

func (s *actionToolState) savedBindings(inv *agent.Invocation) map[string]json.RawMessage {
	if s.saved != nil {
		return s.saved
	}
	s.saved = make(map[string]json.RawMessage)
	if inv.Session == nil {
		return s.saved
	}
	inv.Session.EventMu.RLock()
	defer inv.Session.EventMu.RUnlock()
	for i := range inv.Session.Events {
		e := &inv.Session.Events[i]
		bindings := e.Extensions[actionToolsExtension]
		if len(bindings) == 0 || e.Response == nil || e.Author != inv.AgentName || !e.Filter(inv.GetEventFilterKey()) {
			continue
		}
		for _, choice := range e.Choices {
			msg := choice.Message
			if isActionResult(msg, s.producer) {
				s.saved[msg.ToolID] = bindings
			}
		}
	}
	return s.saved
}

func isActionResult(msg model.Message, info model.Info) bool {
	return msg.Role == model.RoleTool && msg.ProviderData != nil &&
		msg.ProviderData.Protocol == info.ProviderDataProtocol && msg.ProviderData.Model == info.Name
}

func (s *actionToolState) persistedBindings(inv *agent.Invocation, id string) (map[string]bool, error) {
	raw := s.savedBindings(inv)[id]
	if len(raw) == 0 {
		return nil, nil
	}
	var bindings map[string]bool
	if err := json.Unmarshal(raw, &bindings); err != nil {
		return nil, fmt.Errorf("decode tool bindings for action %q: %w", id, err)
	}
	return bindings, nil
}

// actionIdentityKey encodes both components so a top-level name cannot collide
// with a namespace member, regardless of punctuation in either component.
func actionIdentityKey(identity providerdata.ActionToolIdentity) string {
	key, _ := json.Marshal([2]string{identity.Namespace, identity.Name})
	return string(key)
}

// resolveActionTool returns the selected executor and whether it must be
// searched again. A nil executor with no missing binding uses the registered tool.
func resolveActionTool(live map[string]tool.Tool, saved map[string]bool, identity providerdata.ActionToolIdentity) (tool.Tool, bool) {
	key := actionIdentityKey(identity)
	if live != nil {
		if t, ok := live[key]; ok {
			return t, false
		}
	} else if runOnly, ok := saved[key]; ok {
		return nil, runOnly
	}
	// A known search with no matching key cannot authorize a different executor.
	return nil, len(live) > 0 || len(saved) > 0
}

func (s *actionToolState) executionTool(inv *agent.Invocation, binding providerdata.ActionBinding) (tool.Tool, error) {
	live := s.live[binding.ActionID]
	var saved map[string]bool
	if live == nil {
		var err error
		saved, err = s.persistedBindings(inv, binding.ActionID)
		if err != nil {
			return nil, err
		}
	}
	t, missing := resolveActionTool(live, saved, binding.Identity)
	if missing {
		return unavailableActionTool(binding.Name), nil
	}
	return t, nil
}

type actionExecutionContextKey struct{}

type actionExecutionTool struct {
	name string
	tool tool.Tool
}

// withActionExecutionBindings prepares call-scoped execution bindings. The directory and
// published call names remain unchanged, including callback edits.
func withActionExecutionBindings(ctx context.Context, inv *agent.Invocation, req *model.Request, rsp *model.Response) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// A nested agent or a new response starts a separate execution scope.
	if ctx.Value(actionExecutionContextKey{}) != nil {
		ctx = context.WithValue(ctx, actionExecutionContextKey{}, map[string]actionExecutionTool(nil))
	}
	if inv.Model == nil || req == nil || !providerdata.ResponseHasData(rsp) {
		return ctx, nil
	}
	state := actionToolsForInvocation(inv)
	bindingRequest := req
	if state.response == rsp && len(state.justCompleted) > 0 {
		withResults := *req
		withResults.Messages = append(append([]model.Message(nil), req.Messages...), state.justCompleted...)
		bindingRequest = &withResults
	}
	var origins map[string]*actionToolState
	executions := make(map[string]actionExecutionTool)
	for _, choice := range rsp.Choices {
		bindings, err := providerdata.ResolveActionBindings(bindingRequest, choice.Message)
		if err != nil {
			return ctx, err
		}
		for _, binding := range bindings {
			if !hasActionCall(choice.Message.ToolCalls, binding) {
				continue
			}
			if origins == nil {
				origins = actionResultStates(inv, bindingRequest)
			}
			origin := origins[binding.ActionID]
			if origin == nil {
				origin = state
			}
			t, err := origin.executionTool(inv, binding)
			if err != nil {
				return ctx, err
			}
			if t != nil {
				executions[binding.CallID] = actionExecutionTool{name: binding.Name, tool: t}
			}
		}
	}
	if len(executions) == 0 {
		return ctx, nil
	}
	return context.WithValue(ctx, actionExecutionContextKey{}, executions), nil
}

// executionToolsForCall borrows a private directory for one call. Two native
// identities can project to the same registered name without sharing a binding.
func executionToolsForCall(ctx context.Context, call model.ToolCall, tools map[string]tool.Tool) map[string]tool.Tool {
	binding, ok := actionExecutionBinding(ctx, call)
	if !ok {
		return tools
	}
	execution := make(map[string]tool.Tool, len(tools)+1)
	maps.Copy(execution, tools)
	execution[call.Function.Name] = binding
	return execution
}

func executionToolForCall(ctx context.Context, call model.ToolCall, tools map[string]tool.Tool) (tool.Tool, bool) {
	if binding, ok := actionExecutionBinding(ctx, call); ok {
		return binding, true
	}
	t, ok := tools[call.Function.Name]
	return t, ok
}

func actionExecutionBinding(ctx context.Context, call model.ToolCall) (tool.Tool, bool) {
	if ctx == nil {
		return nil, false
	}
	bindings, _ := ctx.Value(actionExecutionContextKey{}).(map[string]actionExecutionTool)
	binding, ok := bindings[call.ID]
	return binding.tool, ok && binding.name == call.Function.Name
}

func hasActionCall(calls []model.ToolCall, binding providerdata.ActionBinding) bool {
	for _, call := range calls {
		if call.ID == binding.CallID && call.Function.Name == binding.Name {
			return true
		}
	}
	return false
}

// A saved schema cannot recreate a Go execution binding. Let the model
// search for it again without dispatching to a similarly named default tool.
type unavailableActionTool string

func (t unavailableActionTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: string(t)}
}

func (t unavailableActionTool) Call(context.Context, []byte) (any, error) {
	return nil, fmt.Errorf("tool %q no longer has its selected execution binding; search for it again", string(t))
}
