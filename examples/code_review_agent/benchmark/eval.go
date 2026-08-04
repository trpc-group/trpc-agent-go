//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main 提供 code_review_agent 的检出率 / 误报率评测（验收标准 2）。
// 样本以统一 diff（.patch）形式存放在 samples/ 下，走真实审查管线
// （ParseDiff -> ScanFile -> Deduplicate -> Report），禁止直连 scanner，
// 确保评测反映端到端行为而非针对规则过拟合。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal"
)

// SampleExpect 描述单个样本期望检出的 rule_id 列表（空表示应零检出）。
type SampleExpect map[string][]string

// Expect 是 samples/expect.json 的结构。
type Expect struct {
	Positive SampleExpect `json:"positive"`
	Negative SampleExpect `json:"negative"`
}

// Detail 是单条样本的评测明细。
type Detail struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"` // "positive" | "negative"
	Pass   bool     `json:"pass"`
	Got    []string `json:"got"` // 实际检出的 rule_id
	Missed []string `json:"missed,omitempty"`
}

// Result 是评测汇总。
type Result struct {
	TotalPositive     int      `json:"total_positive"`
	Hit               int      `json:"hit"`
	TotalNegative     int      `json:"total_negative"`
	FalsePositive     int      `json:"false_positive"`
	Recall            float64  `json:"recall"`              // 检出率
	FalsePositiveRate float64  `json:"false_positive_rate"` // 误报率
	Pass              bool     `json:"pass"`
	Details           []Detail `json:"details"`
}

// MinRecall 与 MaxFalsePosRate 是验收标准 2 的量化阈值。
const (
	MinRecall       = 0.8
	MaxFalsePosRate = 0.15
)

// SamplesDir 是样本根目录（相对 benchmark 包）。
const SamplesDir = "samples"

// samplesRoot 返回样本根目录：兼容 `go test ./benchmark`（cwd=benchmark 包）
// 与 `go run ./benchmark`（cwd=examples/code_review_agent）两种运行方式。
func samplesRoot() (string, error) {
	for _, cand := range []string{
		filepath.Join(SamplesDir, "expect.json"),
		filepath.Join("benchmark", SamplesDir, "expect.json"),
	} {
		if _, err := os.Stat(cand); err == nil {
			return filepath.Dir(cand), nil
		}
	}
	return "", fmt.Errorf("找不到样本目录（samples/ 或 benchmark/samples/ 均不存在）")
}

// LoadExpect 加载 samples/expect.json。
func LoadExpect() (*Expect, error) {
	root, err := samplesRoot()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, "expect.json"))
	if err != nil {
		return nil, fmt.Errorf("读取 expect.json 失败: %w", err)
	}
	var e Expect
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("解析 expect.json 失败: %w", err)
	}
	return &e, nil
}

// Evaluate 运行完整评测，返回结果。
func Evaluate(e *Expect) (*Result, error) {
	root, err := samplesRoot()
	if err != nil {
		return nil, err
	}

	res := &Result{
		TotalPositive: len(e.Positive),
		TotalNegative: len(e.Negative),
	}

	// 正样本：检出至少一个期望 rule 即计为命中。
	for name, wanted := range e.Positive {
		got, err := reviewRuleIDs(filepath.Join(root, "positive", name))
		if err != nil {
			return nil, err
		}
		missed := diffStrings(wanted, got)
		hit := len(missed) < len(wanted)
		if hit {
			res.Hit++
		}
		res.Details = append(res.Details, Detail{
			Name: name, Kind: "positive", Pass: hit,
			Got: got, Missed: missed,
		})
	}

	// 反样本：任何非重复 finding 都算误报。
	for name := range e.Negative {
		got, err := reviewRuleIDs(filepath.Join(root, "negative", name))
		if err != nil {
			return nil, err
		}
		clean := len(got) == 0
		if !clean {
			res.FalsePositive++
		}
		res.Details = append(res.Details, Detail{
			Name: name, Kind: "negative", Pass: clean, Got: got,
		})
	}

	sort.Slice(res.Details, func(i, j int) bool { return res.Details[i].Name < res.Details[j].Name })

	if res.TotalPositive > 0 {
		res.Recall = float64(res.Hit) / float64(res.TotalPositive)
	}
	if res.TotalNegative > 0 {
		res.FalsePositiveRate = float64(res.FalsePositive) / float64(res.TotalNegative)
	}
	res.Pass = res.Recall >= MinRecall && res.FalsePositiveRate <= MaxFalsePosRate
	return res, nil
}

// reviewRuleIDs 对单个 diff 样本跑完整审查管线，返回检出的非重复 rule_id。
func reviewRuleIDs(samplePath string) ([]string, error) {
	content, err := os.ReadFile(samplePath)
	if err != nil {
		return nil, err
	}

	tmp, err := os.MkdirTemp("", "cr-bench-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	task, _, err := internal.RunReview(context.Background(), internal.RunReviewInput{
		InputType:    "diff_file",
		InputContent: string(content),
		DBPath:       filepath.Join(tmp, "bench.db"),
		OutputDir:    tmp,
		DryRun:       true,
	})
	if err != nil {
		return nil, fmt.Errorf("样本 %s 管线失败: %w", filepath.Base(samplePath), err)
	}

	seen := map[string]bool{}
	var ids []string
	for _, f := range task.Findings {
		if !f.IsDuplicate && !seen[f.RuleID] {
			seen[f.RuleID] = true
			ids = append(ids, f.RuleID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// diffStrings 返回 want 中不在 got 里的元素。
func diffStrings(want, got []string) []string {
	gotSet := make(map[string]bool, len(got))
	for _, g := range got {
		gotSet[g] = true
	}
	var missed []string
	for _, w := range want {
		if !gotSet[w] {
			missed = append(missed, w)
		}
	}
	return missed
}
