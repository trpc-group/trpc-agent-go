//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responses

import (
	"fmt"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Existing calls already carry the actual request's projection. Reuse it when
// the native identity is unchanged, or another call had that same identity.
// Rebuilding a plan after a callback cannot safely rerun contextual deferral.
func reconcileProjectedToolNames(request *model.Request, before, after *model.ProviderData, beforeCalls, afterCalls []model.ToolCall) error {
	var oldItems []nativeItem
	var err error
	if before != nil {
		oldItems, err = parseNativeItems(before.Payload, true)
		if err != nil {
			return err
		}
	}
	newItems, err := parseNativeItems(after.Payload, true)
	if err != nil {
		return err
	}

	previous := previousToolNames(oldItems, beforeCalls)
	replacements := make(map[string]string)
	for _, item := range newItems {
		if item.Type != outputTypeFunctionCall {
			continue
		}
		fn := wireFunction{namespace: item.Namespace, name: item.Name}
		var name string
		var mapped bool
		if old, ok := previous.byCall[item.CallID]; ok && old.identity == fn {
			name, mapped = old.name, true
		}
		if !mapped && !previous.conflicting[fn] {
			name, mapped = previous.byIdentity[fn]
		}
		if !mapped && fn.namespace != "" && fn.namespace == fn.name &&
			(previous.conflicting[fn] || ambiguousSelfNamespace(request, fn)) {
			return fmt.Errorf("function call %q in namespace %q is ambiguous after callback: use an empty namespace for a top-level function", fn.name, fn.namespace)
		}
		if mapped {
			replacements[item.CallID] = name
		}
	}
	for i, call := range afterCalls {
		if name, ok := replacements[call.ID]; ok {
			afterCalls[i].Function.Name = name
		}
	}
	return nil
}

type projectedCallName struct {
	identity wireFunction
	name     string
}

type projectedNames struct {
	byCall      map[string]projectedCallName
	byIdentity  map[wireFunction]string
	conflicting map[wireFunction]bool
}

func previousToolNames(items []nativeItem, calls []model.ToolCall) projectedNames {
	names := projectedNames{make(map[string]projectedCallName), make(map[wireFunction]string), make(map[wireFunction]bool)}
	callNames := make(map[string]string, len(calls))
	for _, call := range calls {
		callNames[call.ID] = call.Function.Name
	}
	for _, item := range items {
		name, ok := callNames[item.CallID]
		if !ok || item.Type != outputTypeFunctionCall {
			continue
		}
		fn := wireFunction{namespace: item.Namespace, name: item.Name}
		names.byCall[item.CallID] = projectedCallName{identity: fn, name: name}
		if prior, exists := names.byIdentity[fn]; exists && prior != name {
			names.conflicting[fn] = true
		}
		names.byIdentity[fn] = name
	}
	return names
}

// No request-time context is available here. If a new self-namespaced call can
// refer to both a standalone function and a ToolSet member, their exposure at
// the original model call cannot be reconstructed by invoking deferral again.
func ambiguousSelfNamespace(request *model.Request, fn wireFunction) bool {
	if request == nil {
		return false
	}
	var topKey, memberKey string
	var hasTop, hasMember bool
	for key, candidate := range request.Tools {
		if candidate == nil || candidate.Declaration() == nil {
			continue
		}
		namespace, member, _, isMember := itool.ToolSetMember(candidate)
		if isMember {
			if namespace == fn.namespace && member == fn.name {
				memberKey, hasMember = key, true
			}
		} else if candidate.Declaration().Name == fn.name {
			topKey, hasTop = key, true
		}
	}
	history := scanToolSearchHistory(request.Messages)
	loadedTop := history.loaded[wireFunction{name: fn.name}]
	loadedMember := history.loaded[fn]
	if loadedTop && (hasMember || loadedMember) {
		return true
	}
	if loadedMember && hasTop {
		return true
	}
	return hasTop && hasMember && topKey != memberKey
}
