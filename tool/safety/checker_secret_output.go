//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"regexp"
)

// secretOutputChecker is NOT a standard Checker. It is called
// post-execution (through Desensitize) to scrub secrets from output
// and evidence strings before they are written to audit logs or
// returned to the model.
//
// It is registered in the scanner so the checker reporting shows
// "secret_output:pass", but its Check method always returns nil
// — the actual desensitization happens after Scan returns.
type secretOutputChecker struct {
	policy *Policy
}

func (c *secretOutputChecker) Name() string { return "secret_output" }

func (c *secretOutputChecker) Check(ctx context.Context, req *ScanRequest) (*CheckResult, error) {
	// This checker runs post-execution via Desensitize.
	// Pre-execution secret scanning is handled by secretCmdChecker.
	return nil, nil
}

// Desensitize replaces any matching secret patterns in s with a masked
// version, using the policy's compiled regexes. Both the command and
// the evidence string should be passed through this before audit
// logging or returning to the model.
func Desensitize(s string, patterns []*regexp.Regexp) string {
	if s == "" || len(patterns) == 0 {
		return s
	}
	for _, re := range patterns {
		s = re.ReplaceAllStringFunc(s, maskSecret)
	}
	return s
}
