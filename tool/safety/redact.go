//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

// redactionMask replaces any matched secret in reports, evidence and audit.
const redactionMask = "***REDACTED***"

// redactSecrets replaces every secret-pattern match in text with the mask and
// reports whether any replacement happened. It is idempotent and never returns
// plaintext for a matched secret.
func redactSecrets(text string, secrets []compiledSecret) (string, bool) {
	found := false
	out := text
	for _, s := range secrets {
		if s.re.MatchString(out) {
			found = true
			// The replacement is a literal with no "$" expansion, so
			// ReplaceAllString is safe against capture-group interpolation.
			out = s.re.ReplaceAllString(out, redactionMask)
		}
	}
	return out, found
}

// matchedSecretNames returns the names of every secret pattern that matches
// text, in policy order. Used to build sensitive-leak findings.
func matchedSecretNames(text string, secrets []compiledSecret) []string {
	var names []string
	for _, s := range secrets {
		if s.re.MatchString(text) {
			names = append(names, s.name)
		}
	}
	return names
}
