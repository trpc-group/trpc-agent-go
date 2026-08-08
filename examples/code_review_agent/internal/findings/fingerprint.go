//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package findings

import "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"

// Fingerprint returns the stable versioned identity of a finding location and rule.
func Fingerprint(finding review.Finding) string {
	return finding.ExpectedFingerprint()
}
