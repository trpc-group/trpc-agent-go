//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// UpdatePolicy controls how the built-in Extractor handles potential updates.
type UpdatePolicy string

const (
	// UpdatePolicyCompatible preserves the existing auto-memory behavior.
	UpdatePolicyCompatible UpdatePolicy = "compatible"
	// UpdatePolicyStrict preserves history and permits only safe enrichments.
	UpdatePolicyStrict UpdatePolicy = "strict"
	// UpdatePolicyAddOnly emits only non-duplicate additive writes.
	UpdatePolicyAddOnly UpdatePolicy = "add-only"
)

// WithUpdatePolicy configures how auto memory handles extracted updates.
// Omitting this option or passing an unsupported value uses compatible behavior.
func WithUpdatePolicy(policy UpdatePolicy) Option {
	return func(e *Extractor) {
		e.updatePolicy = normalizeUpdatePolicy(policy)
	}
}

// UpdatePolicy returns the configured auto-memory update policy.
func (e *Extractor) UpdatePolicy() UpdatePolicy {
	return normalizeUpdatePolicy(e.updatePolicy)
}

func normalizeUpdatePolicy(policy UpdatePolicy) UpdatePolicy {
	switch policy {
	case UpdatePolicyCompatible, UpdatePolicyStrict, UpdatePolicyAddOnly:
		return policy
	default:
		return UpdatePolicyCompatible
	}
}

func (e *Extractor) updatePolicyPromptBlock() string {
	switch e.UpdatePolicy() {
	case UpdatePolicyStrict:
		return `

<update_policy>
- Preserve long-term history. Use memory_update only when the new memory is
  the same fact or event with additional non-conflicting detail and retains
  every material detail from the existing memory.
- Use memory_add for corrections, state changes, different events, or any
  uncertain match. Never delete a memory merely because newer information
  differs from it.
- Use memory_delete only when the user explicitly asks to forget or delete
  information. Use memory_clear only when the user explicitly asks to forget
  all stored information.
- Emit no operation for an exact duplicate.
</update_policy>
`
	case UpdatePolicyAddOnly:
		return `

<update_policy>
- Use only memory_add for new information. Do not use memory_update,
  memory_delete, or memory_clear.
- Emit no operation for an exact duplicate.
</update_policy>
`
	default:
		return ""
	}
}

func (e *Extractor) updatePolicyEnabledTools() map[string]struct{} {
	if e.UpdatePolicy() != UpdatePolicyAddOnly {
		return nil
	}
	return map[string]struct{}{
		memory.AddToolName: {},
	}
}

func (e *Extractor) extractionTools() map[string]tool.Tool {
	tools := backgroundTools
	if len(e.enabledTools) > 0 ||
		(e.UpdatePolicy() != UpdatePolicyCompatible && e.enabledTools != nil) {
		tools = filterTools(backgroundTools, e.enabledTools)
	}
	if policyTools := e.updatePolicyEnabledTools(); policyTools != nil {
		tools = filterTools(tools, policyTools)
	}
	return tools
}

func (e *Extractor) updatePolicyToolDescription(name, description string) string {
	if e.UpdatePolicy() == UpdatePolicyStrict && name == memory.DeleteToolName {
		return "Delete a memory only when the user explicitly asks to forget or delete that information."
	}
	return description
}
