//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package systemone_test

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review/systemone"
)

func ExampleNew() {
	reviewer, err := systemone.New("https://api.typesafe.ai",
		systemone.WithModel("jev-1.13.0"),
		systemone.WithAPIKey("your-api-key"),
	)
	if err != nil {
		panic(err)
	}
	approvalPlugin, err := approval.New(approval.WithReviewer(reviewer))
	if err != nil {
		panic(err)
	}
	// Pass this policy through agent.WithToolPermissionPolicy on each run.
	policy := approvalPlugin.PermissionPolicy(nil)
	fmt.Println(policy != nil)
	// Output: true
}
