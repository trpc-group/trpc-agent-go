//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor_test

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func ExampleWithUpdatePolicy() {
	// A real application supplies its extraction model here.
	var extractorModel model.Model
	defaultMemory := memoryinmemory.NewMemoryService(
		memoryinmemory.WithExtractor(extractor.NewExtractor(extractorModel)),
	)
	mergeSimilarMemory := memoryinmemory.NewMemoryService(
		memoryinmemory.WithExtractor(extractor.NewExtractor(
			extractorModel,
			extractor.WithUpdatePolicy(extractor.UpdatePolicyMergeSimilar),
		)),
	)
	preserveHistoryMemory := memoryinmemory.NewMemoryService(
		memoryinmemory.WithExtractor(extractor.NewExtractor(
			extractorModel,
			extractor.WithUpdatePolicy(extractor.UpdatePolicyPreserveHistory),
		)),
	)
	appendOnlyMemory := memoryinmemory.NewMemoryService(
		memoryinmemory.WithExtractor(extractor.NewExtractor(
			extractorModel,
			extractor.WithUpdatePolicy(extractor.UpdatePolicyAppendOnly),
		)),
	)

	for _, service := range []*memoryinmemory.MemoryService{
		defaultMemory,
		mergeSimilarMemory,
		preserveHistoryMemory,
		appendOnlyMemory,
	} {
		_ = service.Close()
	}

	fmt.Println("configured auto memory extractors")
	// Output: configured auto memory extractors
}
