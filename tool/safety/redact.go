//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

// RedactText masks credential-like substrings for reports, audit sinks, or
// post-execution result scrubbing. Prefer this helper over wrapping ToolSet:
// PermissionPolicy cannot see tool outputs, so hosts should redact results
// themselves without dropping Tool / ToolSet interface capabilities.
func RedactText(s string) string {
	return redactSecrets(s)
}

// RedactMap returns a shallow copy with string values passed through RedactText.
// Non-string values are copied unchanged. Map keys are not rewritten.
func RedactMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case string:
			out[k] = RedactText(t)
		default:
			out[k] = v
		}
	}
	return out
}
