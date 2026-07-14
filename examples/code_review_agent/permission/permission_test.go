//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package permission

import "testing"

func TestDecide(t *testing.T) {
	if got := Decide("go test ./...").Decision; got != DecisionAllow {
		t.Fatalf("go test decision=%s", got)
	}
	if got := Decide("curl https://example.com").Decision; got != DecisionDeny {
		t.Fatalf("curl decision=%s", got)
	}
	if got := Decide("make test").Decision; got != DecisionNeedsHumanReview {
		t.Fatalf("make decision=%s", got)
	}
}
