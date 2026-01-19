//
// Tencent is pleased to support the open source community by making trpc-agent-go
// available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"sort"

	skills "trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// maxSkillEnumValues bounds JSON schema size for large repositories.
const maxSkillEnumValues = 256

func skillNameSchema(repo skills.Repository, desc string) *tool.Schema {
	s := &tool.Schema{
		Type:        "string",
		Description: desc,
	}
	s.Enum = skillNameEnum(repo)
	return s
}

func skillNameEnum(repo skills.Repository) []any {
	if repo == nil {
		return nil
	}
	sums := repo.Summaries()
	if len(sums) == 0 || len(sums) > maxSkillEnumValues {
		return nil
	}
	names := make([]string, 0, len(sums))
	for _, sum := range sums {
		if sum.Name == "" {
			continue
		}
		names = append(names, sum.Name)
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	out := make([]any, 0, len(names))
	for _, name := range names {
		out = append(out, name)
	}
	return out
}
