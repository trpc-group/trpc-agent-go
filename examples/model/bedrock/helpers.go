package main

import (
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ============================================================================
// 辅助函数
// ============================================================================

// intPtr 创建 int 指针。
func intPtr(i int) *int { return &i }

// float64Ptr 创建 float64 指针。
func float64Ptr(f float64) *float64 { return &f }

// stringPtr 创建 string 指针。
func stringPtr(s string) *string { return &s }

// printHeader 打印示例标题。
func printHeader(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 68))
	fmt.Printf("  %s\n", title)
	fmt.Println(strings.Repeat("=", 68))
	fmt.Println()
}

// printSubHeader 打印子标题。
func printSubHeader(title string) {
	fmt.Println()
	fmt.Printf("--- %s ---\n\n", title)
}

// printDivider 打印分隔线。
func printDivider() {
	fmt.Println()
	fmt.Println(strings.Repeat("-", 68))
}

// printUsage 打印 token 使用信息。
func printUsage(usage *model.Usage) {
	if usage == nil {
		return
	}
	fmt.Printf("💎 Token 使用 - 输入: %d, 输出: %d, 总计: %d\n",
		usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
}

// printFinishReason 打印结束原因。
func printFinishReason(reason *string) {
	if reason != nil {
		fmt.Printf("🏁 结束原因: %s\n", *reason)
	}
}

// printElapsed 打印耗时。
func printElapsed(start time.Time) {
	fmt.Printf("⏱️  耗时: %v\n", time.Since(start).Round(time.Millisecond))
}
