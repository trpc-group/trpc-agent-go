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
	"trpc.group/trpc-go/trpc-agent-go/memory/internal/updatepolicy"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// UpdatePolicy controls how the built-in extractor handles potential updates.
type UpdatePolicy string

const (
	// UpdatePolicyMergeSimilar preserves the existing similarity-based behavior.
	UpdatePolicyMergeSimilar UpdatePolicy = "merge_similar"
	// UpdatePolicyPreserveHistory permits only non-conflicting enrichments.
	UpdatePolicyPreserveHistory UpdatePolicy = "preserve_history"
	// UpdatePolicyAppendOnly emits only non-duplicate additive writes.
	UpdatePolicyAppendOnly UpdatePolicy = "append_only"
)

// WithUpdatePolicy configures how auto memory handles extracted updates.
// Omitting this option or passing an unsupported value uses Merge Similar.
func WithUpdatePolicy(policy UpdatePolicy) Option {
	return func(e *memoryExtractor) {
		e.updatePolicy = normalizeUpdatePolicy(policy)
	}
}

func (e *memoryExtractor) configuredUpdatePolicy() UpdatePolicy {
	return normalizeUpdatePolicy(e.updatePolicy)
}

// ConfiguredUpdatePolicy carries the built-in extractor policy through an
// internal-only type. External extractors cannot accidentally implement this
// capability because its return type is in an internal package.
func (e *memoryExtractor) ConfiguredUpdatePolicy() updatepolicy.Value {
	return updatepolicy.Value(e.configuredUpdatePolicy())
}

func normalizeUpdatePolicy(policy UpdatePolicy) UpdatePolicy {
	switch policy {
	case UpdatePolicyMergeSimilar,
		UpdatePolicyPreserveHistory,
		UpdatePolicyAppendOnly:
		return policy
	default:
		return UpdatePolicyMergeSimilar
	}
}

func (e *memoryExtractor) updatePolicyPromptBlock() string {
	switch e.configuredUpdatePolicy() {
	case UpdatePolicyPreserveHistory:
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
	case UpdatePolicyAppendOnly:
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

func (e *memoryExtractor) updatePolicyEnabledTools() map[string]struct{} {
	if e.configuredUpdatePolicy() != UpdatePolicyAppendOnly {
		return nil
	}
	return map[string]struct{}{
		memory.AddToolName: {},
	}
}

func (e *memoryExtractor) extractionTools() map[string]tool.Tool {
	tools := backgroundTools
	if len(e.enabledTools) > 0 ||
		(e.configuredUpdatePolicy() != UpdatePolicyMergeSimilar && e.enabledTools != nil) {
		tools = filterTools(backgroundTools, e.enabledTools)
	}
	if policyTools := e.updatePolicyEnabledTools(); policyTools != nil {
		tools = filterTools(tools, policyTools)
	}
	return tools
}

func (e *memoryExtractor) updatePolicyToolDescription(name, description string) string {
	if e.configuredUpdatePolicy() == UpdatePolicyPreserveHistory && name == memory.DeleteToolName {
		return "Delete a memory only when the user explicitly asks to forget or delete that information."
	}
	return description
}
