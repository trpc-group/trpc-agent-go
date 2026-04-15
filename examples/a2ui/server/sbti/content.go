//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import _ "embed"

var (
	//go:embed assets/director_instruction.txt
	sbtiLogicInstructionText string
	//go:embed assets/renderer_instruction.txt
	sbtiRenderInstructionText string
	//go:embed assets/director_output_schema.json
	directorOutputSchemaText string
	//go:embed assets/type_profiles.json
	sbtiTypeProfilesText string
)
