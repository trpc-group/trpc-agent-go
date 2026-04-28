//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newAgent(modelInstance model.Model, generationConfig model.GenerationConfig, documents *documentStore) *llmagent.LLMAgent {
	createDocumentTool := newCreateDocumentTool(documents)
	return llmagent.New(
		"agui-toolcall-delta-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction("You demonstrate streaming tool-call arguments. "+
			"For every user request, call create_document exactly once before answering. "+
			"Generate the requested document yourself and put the full document body in the content argument. "+
			"For demo requests, make the content detailed enough to stream across multiple chunks. "+
			"After the tool succeeds, answer with the saved document id, title, and byte count only."),
		llmagent.WithTools([]tool.Tool{createDocumentTool}),
	)
}
