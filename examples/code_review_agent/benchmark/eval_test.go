//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBenchmark_Thresholds 是 CI 可跑的量化断言：检出率 >= 0.8、误报率 <= 0.15。
func TestBenchmark_Thresholds(t *testing.T) {
	expect, err := LoadExpect()
	if err != nil {
		t.Fatalf("加载评测配置失败: %v", err)
	}
	if len(expect.Positive) == 0 || len(expect.Negative) == 0 {
		t.Fatal("评测集为空，无法断言阈值")
	}

	res, err := Evaluate(expect)
	if err != nil {
		t.Fatalf("评测失败: %v", err)
	}

	if !res.Pass {
		// 输出失败明细，便于定位需要收紧的规则。
		for _, d := range res.Details {
			if d.Pass {
				continue
			}
			t.Errorf("样本 %s (%s) 未达标: got=%v missed=%v", d.Name, d.Kind, d.Got, d.Missed)
		}
		t.Fatalf("评测未达阈值: recall=%.2f (>=%.2f), false_positive=%.2f (<=%.2f)",
			res.Recall, MinRecall, res.FalsePositiveRate, MaxFalsePosRate)
	}

	t.Logf("评测通过: recall=%.2f (%d/%d), false_positive=%.2f (%d/%d)",
		res.Recall, res.Hit, res.TotalPositive,
		res.FalsePositiveRate, res.FalsePositive, res.TotalNegative)
}

// TestExpect_Coverage 校验 expect.json 与样本目录一一对应，防止样本漂移。
func TestExpect_Coverage(t *testing.T) {
	expect, err := LoadExpect()
	if err != nil {
		t.Fatalf("加载评测配置失败: %v", err)
	}

	check := func(dir string, m map[string][]string) {
		entries, err := os.ReadDir(filepath.Join(SamplesDir, dir))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", dir, err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}

		if len(names) != len(m) {
			t.Errorf("%s 目录样本数 %d 与 expect.json 期望 %d 不一致", dir, len(names), len(m))
		}
		for _, name := range names {
			if _, ok := m[name]; !ok {
				t.Errorf("样本 %s/%s 未在 expect.json 中声明", dir, name)
			}
		}
		for name := range m {
			if !containsStr(names, name) {
				t.Errorf("expect.json 中 %s/%s 不存在于磁盘", dir, name)
			}
		}
	}
	check("positive", expect.Positive)
	check("negative", expect.Negative)
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
