//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func checkCommandPermission(command []string) tool.PermissionDecision {
	if len(command) < 2 || command[0] != "go" {
		return tool.DenyPermission("only deterministic Go validation commands are allowed")
	}
	if command[1] != "test" && command[1] != "vet" {
		return tool.DenyPermission("only go test and go vet are allowed")
	}
	for _, argument := range command {
		if strings.ContainsAny(argument, ";|&><`$") {
			return tool.AskPermission("shell metacharacters require human review")
		}
	}
	return tool.AllowPermission()
}

func permissionRecordFor(command []string) permissionRecord {
	decision := checkCommandPermission(command)
	if decision.Action == tool.PermissionActionAllow && decision.Reason == "" {
		decision.Reason = "fixed go test or go vet argument vector passed policy"
	}
	return permissionRecord{Action: string(decision.Action), Reason: decision.Reason, Command: append([]string(nil), command...)}
}
