//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// benchmark 运行 code_review_agent 的检出率 / 误报率评测（验收标准 2）。
//
// 用法:
//
//	go run ./benchmark                          # 打印结果，未达阈值退出码 1
//	go run ./benchmark --json=result.json       # 同时输出结果 JSON
//	go run ./benchmark --recall=0.85 --fp=0.1   # 自定义阈值
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	jsonOut := flag.String("json", "", "评测结果 JSON 输出路径（空则不写文件）")
	recall := flag.Float64("recall", MinRecall, "检出率阈值")
	fp := flag.Float64("fp", MaxFalsePosRate, "误报率阈值")
	flag.Parse()

	expect, err := LoadExpect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载评测配置失败: %v\n", err)
		os.Exit(1)
	}

	res, err := Evaluate(expect)
	if err != nil {
		fmt.Fprintf(os.Stderr, "评测失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("检出率 recall          = %.2f (%d/%d)  阈值 >= %.2f\n",
		res.Recall, res.Hit, res.TotalPositive, *recall)
	fmt.Printf("误报率 false_positive  = %.2f (%d/%d)  阈值 <= %.2f\n",
		res.FalsePositiveRate, res.FalsePositive, res.TotalNegative, *fp)

	for _, d := range res.Details {
		mark := "PASS"
		if !d.Pass {
			mark = "FAIL"
		}
		fmt.Printf("  [%s] %-6s %-45s got=%v", mark, d.Kind, d.Name, d.Got)
		if len(d.Missed) > 0 {
			fmt.Printf(" missed=%v", d.Missed)
		}
		fmt.Println()
	}

	if *jsonOut != "" {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "序列化结果失败: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*jsonOut, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "写结果文件失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("结果已写入 %s\n", *jsonOut)
	}

	pass := res.Recall >= *recall && res.FalsePositiveRate <= *fp
	if !pass {
		fmt.Fprintln(os.Stderr, "评测未达阈值（验收标准 2）")
		os.Exit(1)
	}
	fmt.Println("评测通过：检出率与误报率均达标")
}
