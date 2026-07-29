// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package rules 提供基于 YAML 的规则 DSL（领域特定语言）。
//
// 用户可以用 YAML 文件定义自定义审查规则，不需要写 Go 代码。
//
// 示例规则文件（rules/custom/security.yaml）：
//
//	rules:
//	  - id: SEC-CUSTOM-001
//	    name: "检测硬编码端口"
//	    severity: medium
//	    category: security
//	    description: "端口号不应硬编码在代码中"
//	    match:
//	      token_facts:
//	        - kind: string_literal
//	          value_pattern: "^\\d{4,5}$"
//	        - kind: identifier
//	          value_contains: ["port", "addr"]
//	    exclude:
//	      line_contains: ["localhost", "127.0.0.1", "test"]
//	    message: "疑似硬编码端口号"
//	    recommendation: "使用配置文件或环境变量管理端口"
package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/analyzer"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

// ========== YAML 规则定义 ==========

// DSLRuleFile 是 YAML 规则文件的顶层结构。
type DSLRuleFile struct {
	Rules []DSLRule `yaml:"rules"`
}

// DSLRule 是单条 YAML 规则的定义。
type DSLRule struct {
	ID             string     `yaml:"id"`
	Name           string     `yaml:"name"`
	Severity       string     `yaml:"severity"`
	Category       string     `yaml:"category"`
	Description    string     `yaml:"description"`
	Match          DSLMatch   `yaml:"match"`
	Exclude        DSLExclude `yaml:"exclude"`
	Message        string     `yaml:"message"`
	Recommendation string     `yaml:"recommendation"`
	Confidence     float64    `yaml:"confidence"`
}

// DSLMatch 定义匹配条件。
type DSLMatch struct {
	// Token facts 匹配
	TokenFacts []DSLTokenFact `yaml:"token_facts"`
	// 行内容匹配
	LineContains    []string `yaml:"line_contains"`
	LineNotContains []string `yaml:"line_not_contains"`
	// 文件类型
	FileExtension []string `yaml:"file_extension"`
}

// DSLTokenFact 定义 token fact 的匹配条件。
type DSLTokenFact struct {
	Kind           string   `yaml:"kind"`             // token 类型：identifier, string_literal, assignment 等
	ValueExact     string   `yaml:"value_exact"`      // 精确匹配值
	ValueContains  []string `yaml:"value_contains"`   // 包含任一
	ValuePattern   string   `yaml:"value_pattern"`    // 正则匹配
	ValueNotPrefix string   `yaml:"value_not_prefix"` // 不以此前缀开头
}

// DSLExclude 定义排除条件。
type DSLExclude struct {
	LineContains   []string `yaml:"line_contains"`
	LineStartsWith []string `yaml:"line_starts_with"`
	FileContains   []string `yaml:"file_contains"`
}

// ========== DSL 规则实现 ==========

// DSLRuleInstance 是从 YAML 加载的规则实例。
type DSLRuleInstance struct {
	def      DSLRule
	analyzer *analyzer.TokenAnalyzer
	// 预编译的正则
	valueRegex   *regexp.Regexp
	excludeRegex []*regexp.Regexp
}

// NewDSLRuleInstance 从 YAML 定义创建规则实例。
func NewDSLRuleInstance(def DSLRule) (*DSLRuleInstance, error) {
	r := &DSLRuleInstance{
		def:      def,
		analyzer: analyzer.NewTokenAnalyzer(),
	}

	// 预编译 value_pattern 正则
	for _, tf := range def.Match.TokenFacts {
		if tf.ValuePattern != "" {
			re, err := regexp.Compile(tf.ValuePattern)
			if err != nil {
				return nil, fmt.Errorf("规则 %s 的 value_pattern 正则无效: %w", def.ID, err)
			}
			r.valueRegex = re
		}
	}

	return r, nil
}

func (r *DSLRuleInstance) ID() string                  { return r.def.ID }
func (r *DSLRuleInstance) Name() string                { return r.def.Name }
func (r *DSLRuleInstance) Severity() findings.Severity { return findings.Severity(r.def.Severity) }
func (r *DSLRuleInstance) Category() findings.Category { return findings.Category(r.def.Category) }

func (r *DSLRuleInstance) Check(fd diff.FileDiff) ([]findings.Finding, error) {
	var result []findings.Finding

	// 检查文件扩展名
	if len(r.def.Match.FileExtension) > 0 {
		matched := false
		for _, ext := range r.def.Match.FileExtension {
			if strings.HasSuffix(fd.NewPath, ext) {
				matched = true
				break
			}
		}
		if !matched {
			return nil, nil
		}
	}

	for _, line := range fd.AddedLines() {
		content := line.Content
		if content == "" {
			continue
		}

		// 排除条件
		if r.isExcluded(content) {
			continue
		}

		// 行内容匹配
		if !r.matchLineContent(content) {
			continue
		}

		// Token facts 匹配
		analysis := r.analyzer.AnalyzeLine(content, line.NewLine)
		if !r.matchTokenFacts(analysis) {
			continue
		}

		// 命中！
		confidence := r.def.Confidence
		if confidence == 0 {
			confidence = 0.80
		}

		f := findings.NewFinding(
			r.Severity(), r.Category(), r.ID(),
			r.def.Message,
			fd.NewPath, line.NewLine,
			content,
			r.def.Recommendation,
			confidence,
			"dsl:"+r.def.ID,
		)
		result = append(result, *f)
	}

	return result, nil
}

// isExcluded 检查是否满足排除条件。
func (r *DSLRuleInstance) isExcluded(content string) bool {
	lower := strings.ToLower(content)

	for _, pattern := range r.def.Exclude.LineContains {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}

	for _, prefix := range r.def.Exclude.LineStartsWith {
		trimmed := strings.TrimSpace(content)
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	return false
}

// matchLineContent 检查行内容是否满足匹配条件。
func (r *DSLRuleInstance) matchLineContent(content string) bool {
	lower := strings.ToLower(content)

	// line_contains：必须包含任一
	if len(r.def.Match.LineContains) > 0 {
		matched := false
		for _, pattern := range r.def.Match.LineContains {
			if strings.Contains(lower, strings.ToLower(pattern)) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// line_not_contains：不能包含任一
	for _, pattern := range r.def.Match.LineNotContains {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return false
		}
	}

	return true
}

// matchTokenFacts 检查 token facts 是否满足匹配条件。
func (r *DSLRuleInstance) matchTokenFacts(analysis analyzer.TokenAnalysis) bool {
	if len(r.def.Match.TokenFacts) == 0 {
		return true // 没有 token 条件，跳过
	}

	for _, tf := range r.def.Match.TokenFacts {
		if !r.matchSingleTokenFact(analysis, tf) {
			return false
		}
	}
	return true
}

// matchSingleTokenFact 检查单个 token fact 条件。
func (r *DSLRuleInstance) matchSingleTokenFact(analysis analyzer.TokenAnalysis, tf DSLTokenFact) bool {
	for _, fact := range analysis.Facts {
		// 检查 kind
		if string(fact.Kind) != tf.Kind {
			continue
		}

		// value_exact
		if tf.ValueExact != "" && fact.Value != tf.ValueExact {
			continue
		}

		// value_contains
		if len(tf.ValueContains) > 0 {
			matched := false
			for _, vc := range tf.ValueContains {
				if strings.Contains(strings.ToLower(fact.Value), strings.ToLower(vc)) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// value_pattern
		if r.valueRegex != nil && !r.valueRegex.MatchString(fact.Value) {
			continue
		}

		// value_not_prefix
		if tf.ValueNotPrefix != "" && strings.HasPrefix(fact.Value, tf.ValueNotPrefix) {
			continue
		}

		return true
	}

	return false
}

// ========== 规则加载 ==========

// LoadDSLRules 从目录加载所有 YAML 规则文件。
func LoadDSLRules(dir string) ([]Rule, error) {
	var rules []Rule

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		loaded, err := LoadDSLRulesFromFile(path)
		if err != nil {
			return fmt.Errorf("加载规则文件 %s 失败: %w", path, err)
		}
		rules = append(rules, loaded...)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return rules, nil
}

// LoadDSLRulesFromFile 从单个 YAML 文件加载规则。
func LoadDSLRulesFromFile(path string) ([]Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	return LoadDSLRulesFromBytes(data)
}

// LoadDSLRulesFromBytes 从 YAML 字节加载规则。
func LoadDSLRulesFromBytes(data []byte) ([]Rule, error) {
	var file DSLRuleFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("解析 YAML 失败: %w", err)
	}

	var rules []Rule
	for _, def := range file.Rules {
		// 验证必填字段
		if def.ID == "" {
			return nil, fmt.Errorf("规则缺少 id 字段")
		}
		if def.Name == "" {
			return nil, fmt.Errorf("规则 %s 缺少 name 字段", def.ID)
		}
		if def.Severity == "" {
			def.Severity = "medium"
		}
		if def.Category == "" {
			def.Category = "security"
		}

		instance, err := NewDSLRuleInstance(def)
		if err != nil {
			return nil, err
		}
		rules = append(rules, instance)
	}

	return rules, nil
}
