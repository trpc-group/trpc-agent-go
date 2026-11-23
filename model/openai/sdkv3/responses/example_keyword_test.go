//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses_test

import (
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses/toolsearch"
)

func ExampleWithClientToolSearch_keyword() {
	search := toolsearch.NewKeyword(toolsearch.WithKeywordMaxResults(3))
	_ = responses.New("gpt-5.4", responses.WithClientToolSearch(search))
}
