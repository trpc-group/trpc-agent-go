//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"encoding/json"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// RiskLevel represents the severity of a detected safety issue.
type RiskLevel string

const (
	// RiskLevelNone indicates no safety risks detected.
	RiskLevelNone RiskLevel = "none"
	// RiskLevelLow indicates minor potential concern.
	RiskLevelLow RiskLevel = "low"
	// RiskLevelMedium indicates moderate risk requiring caution or confirmation.
	RiskLevelMedium RiskLevel = "medium"
	// RiskLevelHigh indicates significant risk that should typically be blocked.
	RiskLevelHigh RiskLevel = "high"
	// RiskLevelCritical indicates severe security hazard (e.g., system destruction, credential leak).
	RiskLevelCritical RiskLevel = "critical"
)

// Report represents a structured safety scan report for a tool invocation.
type Report struct {
	ToolName       string                `json:"tool_name"`
	Command        string                `json:"command"`
	Backend        string                `json:"backend"`
	Decision       tool.PermissionAction `json:"decision"`
	RiskLevel      RiskLevel             `json:"risk_level"`
	RuleID         string                `json:"rule_id"`
	Evidence       string                `json:"evidence"`
	Recommendation string                `json:"recommendation"`
	IsBlocked      bool                  `json:"is_blocked"`
}

// SaveJSON writes the report formatted as JSON to the given path.
func (r *Report) SaveJSON(filePath string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report failed: %w", err)
	}
	if err := os.WriteFile(filePath, data, 0600); err != nil {
		return fmt.Errorf("write report file failed: %w", err)
	}
	return nil
}
