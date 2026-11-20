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

// Only binding names and their lifetime cross the persistence boundary. The
// tool objects themselves stay in the invocation that performed the search.
const actionToolsExtension = "trpc.provider_action.tool_bindings.v1"

type actionToolState struct {
	live          map[string]map[string]tool.Tool // result call ID -> execution name -> tool
	saved         map[string]json.RawMessage
	response      *model.Response
	justCompleted []string // results emitted after the current mixed response's request
}

func actionToolsForInvocation(inv *agent.Invocation) *actionToolState {
	info := inv.Model.Info()
	key := fmt.Sprintf("model:action-tools:%q:%q", info.ProviderDataProtocol, info.Name)
	if state, ok := agent.GetStateValue[*actionToolState](inv, key); ok {
		return state
	}
	state := &actionToolState{live: make(map[string]map[string]tool.Tool)}
	inv.SetState(key, state)
	return state
}

// collect stages the bindings for one action batch. A nil value denotes the
// registered tool, which should follow the next request's active directory.
func (s *actionToolState) collect(ctx context.Context, inv *agent.Invocation, req *model.Request) (
	context.Context, map[string]map[string]tool.Tool, error,
) {
	overrides, err := s.overrides(inv, req, nil)
	if err != nil {
		return nil, nil, err
	}
	missing := make(map[string]bool)
	for name, t := range overrides {
		if t == nil {
			missing[name] = true
		}
	}
	staged := make(map[string]map[string]tool.Tool)
	actions := &providerdata.ActionTools{
		Unavailable: missing,
		Bind: func(id, name string, t tool.Tool) bool {
			// Compare the interface as offered and returned, including concrete
			// types that cannot be compared with ==. Selection of an original
			// entry needs neither another filter call nor a run-only executor.
			if req != nil && reflect.ValueOf(req.Tools[name]) == reflect.ValueOf(t) {
				if _, replacing := overrides[name]; !replacing {
					return true
				}
				t = nil
			} else if filter := inv.RunOptions.ToolFilter; filter != nil && !filter(ctx, itool.ResolveDeclaration(t)) {
				return false
			}
			if staged[id] == nil {
				staged[id] = make(map[string]tool.Tool)
			}
			staged[id][name] = t
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
	for name, t := range bindings {
		runOnly[name] = t != nil
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
	info := inv.Model.Info()
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
			if isActionResult(msg, info) {
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

// overrides folds visible action results in conversation order. A missing
// entry uses the request's registered tool; a nil entry was bound in a
// previous run and must be searched for again.
func (s *actionToolState) overrides(inv *agent.Invocation, req *model.Request, rsp *model.Response) (map[string]tool.Tool, error) {
	if req == nil {
		return nil, nil
	}
	info := inv.Model.Info()
	var ids []string
	for _, msg := range req.Messages {
		if isActionResult(msg, info) {
			ids = append(ids, msg.ToolID)
		}
	}
	if rsp != nil && s.response == rsp {
		ids = append(ids, s.justCompleted...)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	overrides := make(map[string]tool.Tool)
	for _, id := range ids {
		if bindings, ok := s.live[id]; ok {
			for name, t := range bindings {
				if t == nil {
					delete(overrides, name)
				} else {
					overrides[name] = t
				}
			}
			continue
		}
		raw := s.savedBindings(inv)[id]
		if len(raw) == 0 {
			continue
		}
		var runOnly map[string]bool
		if err := json.Unmarshal(raw, &runOnly); err != nil {
			return nil, fmt.Errorf("decode tool bindings for action %q: %w", id, err)
		}
		for name, required := range runOnly {
			if required {
				overrides[name] = nil
			} else {
				delete(overrides, name)
			}
		}
	}
	return overrides, nil
}

// executionRequest overlays the tools for execution without changing the
// request seen by the model or the shared Agent directory.
func executionRequest(inv *agent.Invocation, req *model.Request, rsp *model.Response) (*model.Request, *model.Response, error) {
	if inv.Model == nil || req == nil || !providerdata.ResponseHasData(rsp) {
		return req, rsp, nil
	}
	state := actionToolsForInvocation(inv)
	overrides, err := state.overrides(inv, req, rsp)
	if err != nil || len(overrides) == 0 {
		return req, rsp, err
	}
	exec := *req
	exec.Tools = make(map[string]tool.Tool, len(req.Tools)+len(overrides))
	maps.Copy(exec.Tools, req.Tools)
	for name, t := range overrides {
		if t == nil {
			t = unavailableActionTool(name)
		}
		exec.Tools[name] = t
	}
	// A qualified provider call can still refer to a prior external binding
	// after a registered ToolSet with the same wire name becomes visible. Use
	// the hint only when that exact action result recorded the binding. Rewrite
	// a private response copy so another call to the registered key is not
	// redirected and the published native projection stays unchanged.
	var executionResponse *model.Response
	for i := range rsp.Choices {
		for j, call := range rsp.Choices[i].Message.ToolCalls {
			id, name := providerdata.ActionBindingHint(call)
			if _, active := overrides[name]; id == "" || name == "" || !active || !state.boundBy(inv, id, name) {
				continue
			}
			if executionResponse == nil {
				executionResponse = rsp.Clone()
				for k := range executionResponse.Choices {
					executionResponse.Choices[k].Message = providerdata.CloneMessageProjections(executionResponse.Choices[k].Message)
				}
			}
			executionResponse.Choices[i].Message.ToolCalls[j].Function.Name = name
		}
	}
	if executionResponse == nil {
		executionResponse = rsp
	}
	return &exec, executionResponse, nil
}

func (s *actionToolState) boundBy(inv *agent.Invocation, id, name string) bool {
	if bindings, ok := s.live[id]; ok {
		_, exists := bindings[name]
		return exists
	}
	var bindings map[string]bool
	if err := json.Unmarshal(s.savedBindings(inv)[id], &bindings); err != nil {
		return false
	}
	_, exists := bindings[name]
	return exists
}

// A saved schema cannot recreate a Go execution binding. Let the model
// search for it again without dispatching to a similarly named default tool.
type unavailableActionTool string

func (t unavailableActionTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: string(t)}
}

func (t unavailableActionTool) Call(context.Context, []byte) (any, error) {
	return nil, fmt.Errorf("tool %q was bound by a previous run; search for it again", string(t))
}
