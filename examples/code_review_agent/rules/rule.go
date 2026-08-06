// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package rules 提供代码审查规则引擎。
// 每条规则实现 Rule 接口，RuleEngine 负责调度执行。
package rules

import (
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

// Rule 是一条代码审查规则的接口。
//
// 每条规则负责检查一种特定的代码问题，比如"硬编码密钥"或"goroutine 泄漏"。
// 规则接收一个文件的 diff，返回它发现的问题列表。
//
// 实现示例：
//
//	type HardcodedSecretRule struct{}
//
//	func (r *HardcodedSecretRule) ID() string { return "SEC-001" }
//	func (r *HardcodedSecretRule) Name() string { return "硬编码密钥检测" }
//	// ... 其他方法
type Rule interface {
	// ID 返回规则的唯一编号，如 "SEC-001"、"RES-001"。
	ID() string

	// Name 返回规则的可读名称，如 "硬编码密钥检测"。
	Name() string

	// Severity 返回该规则发现问题时的默认严重级别。
	Severity() findings.Severity

	// Category 返回该规则的问题分类。
	Category() findings.Category

	// Check 执行审查，对一个文件的 diff 进行检查。
	//
	// 参数：
	//   - fd: 一个文件的 diff 数据
	//
	// 返回：
	//   - 该规则发现的问题列表（可能为空）
	//   - 如果检查过程出错，返回 error
	Check(fd diff.FileDiff) ([]findings.Finding, error)
}

// MultiFileRule 是支持跨文件检查的规则接口。
//
// 某些规则需要看到所有文件才能正确判断（如测试缺失检测）。
// 如果一个规则同时实现了 Rule 和 MultiFileRule，引擎会优先使用 CheckFiles。
type MultiFileRule interface {
	Rule

	// CheckFiles 对多个文件执行检查（可以跨文件关联信息）。
	CheckFiles(files []diff.FileDiff) ([]findings.Finding, error)
}
