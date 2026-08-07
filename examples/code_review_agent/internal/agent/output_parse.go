//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// decodeSkillRunOutput 把 trpc-agent-go skill_run 输出转换成本地摘要。
func decodeSkillRunOutput(raw any) (skillRunOutput, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return skillRunOutput{}, err
	}
	var out skillRunOutput
	if err := json.Unmarshal(b, &out); err != nil {
		return skillRunOutput{}, err
	}
	return out, nil
}

type skillRunOutput struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out"`
	DurationMS int64  `json:"duration_ms"`
}

// parseSkillFindings 从 Skill stdout 契约中解析结构化 findings。
func parseSkillFindings(stdout string) (review.Result, error) {
	var payload struct {
		Findings []review.Finding `json:"findings"`
		Warnings []review.Finding `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
		return review.Result{}, err
	}
	for i := range payload.Findings {
		payload.Findings[i] = sanitizeFinding(payload.Findings[i])
	}
	for i := range payload.Warnings {
		payload.Warnings[i] = sanitizeFinding(payload.Warnings[i])
	}
	return review.Result{
		Findings: review.DedupeFindings(payload.Findings),
		Warnings: review.DedupeFindings(payload.Warnings),
	}, nil
}

func sanitizeFinding(f review.Finding) review.Finding {
	f.Evidence = review.RedactSecrets(f.Evidence)
	if f.Status == "" {
		f.Status = "finding"
	}
	return f
}

func newTaskID(diff []byte) string {
	return "task-" + digestBytes(diff)[:12] + fmt.Sprintf("-%d", time.Now().UnixNano())
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func digestString(data string) string {
	return digestBytes([]byte(data))
}

func sandboxCommandOutput(raw any) commandOutput {
	b, err := json.Marshal(raw)
	if err != nil {
		return commandOutput{}
	}
	var out struct {
		Status   string `json:"status"`
		Output   string `json:"output"`
		ExitCode *int   `json:"exit_code"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return commandOutput{}
	}
	return commandOutput{
		Status:   out.Status,
		Text:     out.Output,
		ExitCode: out.ExitCode,
	}
}

func sandboxRunOutput(text string, limit int) string {
	text = review.RedactSecrets(text)
	if limit > 0 && len(text) > limit {
		text = text[:limit]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text
}

type commandOutput struct {
	Status   string
	Text     string
	ExitCode *int
}
