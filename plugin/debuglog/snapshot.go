//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package debuglog

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func jsonValue(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func addJSONSnapshot(payload map[string]any, key, errorKey string, v any) {
	value, err := jsonValue(v)
	if err != nil {
		payload[errorKey] = err.Error()
		payload[key+"_type"] = typeName(v)
		return
	}
	payload[key] = value
}

func addRequestSnapshot(payload map[string]any, req *model.Request) {
	addJSONSnapshot(payload, "request", "request_encode_error", req)
	if req == nil {
		return
	}
	supplement := requestSupplement(req)
	if len(supplement) > 0 {
		payload["request_supplement"] = supplement
	}
}

func requestSupplement(req *model.Request) map[string]any {
	supplement := map[string]any{}
	addRequestToolsSupplement(supplement, req.Tools)
	addJSONMapSnapshot(supplement, "extra_fields", "extra_field_errors", req.ExtraFields)
	if len(req.Headers) > 0 {
		supplement["headers"] = stringMapToAny(req.Headers)
	}
	return supplement
}

func addRequestToolsSupplement(supplement map[string]any, tools map[string]tool.Tool) {
	if len(tools) == 0 {
		return
	}
	values := map[string]any{}
	errs := map[string]any{}
	for _, name := range sortedKeys(tools) {
		decl, err := safeToolDeclaration(tools[name])
		if err != nil {
			errs[name] = map[string]any{
				"error": err.Error(),
				"type":  typeName(tools[name]),
			}
			continue
		}
		value, err := jsonValue(decl)
		if err != nil {
			errs[name] = map[string]any{
				"error": err.Error(),
				"type":  typeName(decl),
			}
			continue
		}
		values[name] = value
	}
	if len(values) > 0 {
		supplement["tools"] = values
	}
	if len(errs) > 0 {
		supplement["tool_errors"] = errs
	}
}

func addToolDeclarationSnapshot(payload map[string]any, decl *tool.Declaration) {
	addJSONSnapshot(payload, "declaration", "declaration_encode_error", decl)
}

func addToolArgumentsSnapshot(payload map[string]any, args []byte) {
	payload["arguments_bytes"] = len(args)
	var value any
	if err := json.Unmarshal(args, &value); err != nil {
		payload["arguments_text"] = string(args)
		payload["arguments_error"] = err.Error()
		return
	}
	payload["arguments"] = value
}

func addJSONMapSnapshot(
	payload map[string]any,
	key string,
	errorKey string,
	values map[string]any,
) {
	if len(values) == 0 {
		return
	}
	out := map[string]any{}
	errs := map[string]any{}
	for _, name := range sortedKeys(values) {
		value, err := jsonValue(values[name])
		if err != nil {
			errs[name] = map[string]any{
				"error": err.Error(),
				"type":  typeName(values[name]),
			}
			continue
		}
		out[name] = value
	}
	if len(out) > 0 {
		payload[key] = out
	}
	if len(errs) > 0 {
		payload[errorKey] = errs
	}
}

func addEventSnapshot(payload map[string]any, e *event.Event) {
	addJSONSnapshot(payload, "event", "event_encode_error", e)
	if e == nil {
		return
	}
	supplement := map[string]any{}
	if e.StructuredOutput != nil {
		addJSONSnapshot(supplement, "structured_output", "structured_output_encode_error", e.StructuredOutput)
	}
	if e.ExecutionTrace != nil {
		addJSONSnapshot(supplement, "execution_trace", "execution_trace_encode_error", e.ExecutionTrace)
	}
	if len(supplement) > 0 {
		payload["event_supplement"] = supplement
	}
}

func addFullResponseEventSnapshot(payload map[string]any, e *event.Event) {
	if e == nil {
		return
	}
	addJSONSnapshot(payload, "full_response_event", "full_response_event_encode_error", e)
}

func invocationSnapshot(inv *agent.Invocation) map[string]any {
	out := map[string]any{
		"agent_name":                inv.AgentName,
		"branch":                    inv.Branch,
		"end_invocation":            inv.EndInvocation,
		"invocation_id":             inv.InvocationID,
		"structured_output_present": inv.StructuredOutput != nil,
	}
	if parent := inv.GetParentInvocation(); parent != nil {
		out["parent_invocation_id"] = parent.InvocationID
	}
	if inv.Session != nil {
		out["session"] = map[string]any{
			"app_name": inv.Session.AppName,
			"id":       inv.Session.ID,
			"user_id":  inv.Session.UserID,
		}
	}
	if inv.Model != nil {
		out["model_name"] = inv.Model.Info().Name
	}
	runOptions := map[string]any{
		"app_name":                inv.RunOptions.AppName,
		"disable_tracing":         inv.RunOptions.DisableTracing,
		"event_filter_key":        inv.RunOptions.EventFilterKey,
		"execution_trace_enabled": inv.RunOptions.ExecutionTraceEnabled,
		"request_id":              inv.RunOptions.RequestID,
		"resume":                  inv.RunOptions.Resume,
		"stream_mode_enabled":     inv.RunOptions.StreamModeEnabled,
		"stream_modes":            streamModes(inv.RunOptions.StreamModes),
	}
	out["run_options"] = runOptions
	return out
}

func applyInvocationFields(ent *entry, inv *agent.Invocation) {
	if inv == nil {
		return
	}
	ent.InvocationID = inv.InvocationID
	ent.AgentName = inv.AgentName
	if parent := inv.GetParentInvocation(); parent != nil {
		ent.ParentInvocationID = parent.InvocationID
	}
	if inv.Session != nil {
		ent.SessionID = inv.Session.ID
		ent.UserID = inv.Session.UserID
	}
	if inv.Model != nil {
		ent.ModelName = inv.Model.Info().Name
	}
}

func safeToolDeclaration(t tool.Tool) (decl *tool.Declaration, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool declaration panic: %v", recovered)
		}
	}()
	if t == nil {
		return nil, errors.New("tool is nil")
	}
	decl = t.Declaration()
	if decl == nil {
		return nil, errors.New("tool declaration is nil")
	}
	return decl, nil
}

func stringMapToAny(values map[string]string) map[string]any {
	out := make(map[string]any, len(values))
	for _, key := range sortedKeys(values) {
		out[key] = values[key]
	}
	return out
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func streamModes(modes []agent.StreamMode) []string {
	out := make([]string, 0, len(modes))
	for _, mode := range modes {
		out = append(out, string(mode))
	}
	return out
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func typeName(v any) string {
	if v == nil {
		return "nil"
	}
	return reflect.TypeOf(v).String()
}
