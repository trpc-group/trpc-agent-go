// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package findings

import "sort"

// Deduplicate 对 findings 列表去重，并按置信度分为高置信和低置信两组。
//
// 去重规则：
//   - 同一文件 + 同一行 + 同一分类 + 同一规则ID → 只保留置信度最高的那条
//
// 分组规则：
//   - 置信度 >= 0.7 → findings（正式发现）
//   - 置信度 < 0.7 → warnings（需要人工复核）
type DedupResult struct {
	Findings []Finding // 高置信度发现（正式结果）
	Warnings []Finding // 低置信度发现（需人工复核）
	Removed  int       // 被去重移除的数量
}

// Deduplicate 执行去重和分组。
func Deduplicate(input []Finding) DedupResult {
	if len(input) == 0 {
		return DedupResult{}
	}

	// 第一步：按 dedupKey 分组，保留每组中置信度最高的
	best := make(map[string]Finding)
	removed := 0

	for _, f := range input {
		key := f.DedupKey()
		if existing, ok := best[key]; ok {
			// 同 key 已存在，保留置信度更高的
			if f.Confidence > existing.Confidence {
				best[key] = f
				removed++
			} else {
				removed++
			}
		} else {
			best[key] = f
		}
	}

	// 第二步：按置信度分为 findings 和 warnings
	var findings, warnings []Finding
	for _, f := range best {
		if f.IsHighConfidence() {
			findings = append(findings, f)
		} else {
			warnings = append(warnings, f)
		}
	}

	// 第三步：按严重级别降序、行号升序排序
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].SeverityOrder() != findings[j].SeverityOrder() {
			return findings[i].SeverityOrder() > findings[j].SeverityOrder()
		}
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})

	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].SeverityOrder() != warnings[j].SeverityOrder() {
			return warnings[i].SeverityOrder() > warnings[j].SeverityOrder()
		}
		if warnings[i].File != warnings[j].File {
			return warnings[i].File < warnings[j].File
		}
		return warnings[i].Line < warnings[j].Line
	})

	return DedupResult{
		Findings: findings,
		Warnings: warnings,
		Removed:  removed,
	}
}
