// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package rules

import (
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

// RuleEngine 是规则引擎，负责注册和执行所有审查规则。
type RuleEngine struct {
	rules []Rule
}

// NewEngine 创建一个新的规则引擎。
func NewEngine() *RuleEngine {
	return &RuleEngine{}
}

// Register 注册一条规则。
//
// 示例：
//
//	engine := rules.NewEngine()
//	engine.Register(&HardcodedSecretRule{})
//	engine.Register(&GoroutineLeakRule{})
func (e *RuleEngine) Register(rule Rule) {
	e.rules = append(e.rules, rule)
}

// RegisterAll 批量注册规则。
func (e *RuleEngine) RegisterAll(rules ...Rule) {
	for _, r := range rules {
		e.Register(r)
	}
}

// Rules 返回所有已注册的规则。
func (e *RuleEngine) Rules() []Rule {
	return e.rules
}

// Run 对一组文件 diff 执行所有已注册的规则。
//
// 执行流程：
//  1. 先执行多文件规则（CheckFiles），可以跨文件关联信息
//  2. 再遍历每个文件 × 每条单文件规则（Check）
//  3. 收集所有 findings
//
// 返回所有规则发现的问题的合并列表。
func (e *RuleEngine) Run(files []diff.FileDiff) ([]findings.Finding, error) {
	var allFindings []findings.Finding

	// 第一步：执行多文件规则
	for _, rule := range e.rules {
		if mfr, ok := rule.(MultiFileRule); ok {
			results, err := mfr.CheckFiles(files)
			if err != nil {
				log.Printf("[规则引擎] 多文件规则 %s(%s) 执行出错: %v",
					rule.Name(), rule.ID(), err)
				continue
			}
			allFindings = append(allFindings, results...)
		}
	}

	// 第二步：执行单文件规则
	for _, fd := range files {
		for _, rule := range e.rules {
			// 多文件规则已经在上面执行过，跳过
			if _, ok := rule.(MultiFileRule); ok {
				continue
			}
			results, err := rule.Check(fd)
			if err != nil {
				log.Printf("[规则引擎] 规则 %s(%s) 在文件 %s 执行出错: %v",
					rule.Name(), rule.ID(), fd.NewPath, err)
				continue
			}
			allFindings = append(allFindings, results...)
		}
	}

	return allFindings, nil
}

// RunOnSingleFile 对单个文件执行所有规则（方便测试）。
func (e *RuleEngine) RunOnSingleFile(fd diff.FileDiff) ([]findings.Finding, error) {
	return e.Run([]diff.FileDiff{fd})
}

// Summary 返回引擎的规则摘要（用于日志和调试）。
func (e *RuleEngine) Summary() string {
	s := fmt.Sprintf("规则引擎: 已注册 %d 条规则\n", len(e.rules))
	for i, r := range e.rules {
		s += fmt.Sprintf("  %d. [%s] %s (%s, %s)\n",
			i+1, r.ID(), r.Name(), r.Severity(), r.Category())
	}
	return s
}
