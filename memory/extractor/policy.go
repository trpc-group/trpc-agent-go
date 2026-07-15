//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

// UpdatePolicy controls how the built-in Extractor handles potential updates.
type UpdatePolicy string

const (
	// UpdatePolicyCompatible preserves the existing auto-memory behavior.
	UpdatePolicyCompatible UpdatePolicy = "compatible"
	// UpdatePolicyStrict updates only strict, non-conflicting enrichments.
	UpdatePolicyStrict UpdatePolicy = "strict"
	// UpdatePolicyAddOnly converts auto-extracted updates into additive writes.
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
- Emit no operation for an exact duplicate.
</update_policy>
`
	case UpdatePolicyAddOnly:
		return `

<update_policy>
- Preserve long-term history and use memory_add for new information.
- Do not use memory_update. Emit no operation for an exact duplicate.
- Never delete a memory merely because newer information differs from it.
</update_policy>
`
	default:
		return ""
	}
}
