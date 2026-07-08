// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import "strings"

func (s *Scanner) scanDependencyArgv(argv []string, loc string) []Finding {
	if len(argv) < 2 {
		return nil
	}
	cmd := normalizeCommandName(argv[0])
	sub := strings.ToLower(argv[1])
	for _, dep := range s.policy.DependencyCommands {
		if normalizeCommandName(dep.Command) != cmd {
			continue
		}
		for _, candidate := range dep.Subcommands {
			if strings.EqualFold(candidate, sub) {
				action := dep.Action
				if action == "" {
					action = DecisionAsk
				}
				return []Finding{finding(
					RuleDependencyInstall, CategoryDependency, RiskMedium, action,
					"dependency or environment change command: "+strings.Join(argv, " "),
					loc,
					"Review package source, pin versions, and prefer approved mirrors before installing.",
				)}
			}
		}
	}
	return nil
}
