//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summaryinject

import (
	"fmt"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Long-context stand-in: each message is several kilobytes so a miss walks
// total request bytes rather than a handful of short strings.
const benchMessageBytes = 4096

func benchSummaryBlock() string {
	return "Here is a brief summary of your previous interactions:\n\n" +
		"<summary_of_previous_interactions>\n" +
		strings.Repeat("topic-billing-refund-policy ", 12) +
		"\n</summary_of_previous_interactions>\n"
}

func benchHistoryBody() string {
	return strings.Repeat("user-and-assistant-turn-payload-", benchMessageBytes/32)
}

func benchMessages(n int, blockAtPrefix bool, block string) []model.Message {
	body := benchHistoryBody()
	messages := make([]model.Message, n)
	for i := range messages {
		content := body
		if blockAtPrefix && i == 0 {
			content = block + body
		}
		role := model.RoleUser
		if i%2 == 0 {
			role = model.RoleAssistant
		}
		if i == 0 {
			role = model.RoleSystem
		}
		messages[i] = model.Message{Role: role, Content: content}
	}
	return messages
}

// blockPresentNoop is the diagnostics-off baseline: no request scan.
func blockPresentNoop(Selection, []model.Message) bool { return false }

// blockPresentNaive is strings.Contains with no length guard.
func blockPresentNaive(s Selection, messages []model.Message) bool {
	if !s.Selected || s.Block == "" {
		return false
	}
	for i := range messages {
		if strings.Contains(messages[i].Content, s.Block) {
			return true
		}
	}
	return false
}

// blockPresentPrefixThenContains is the discarded HasPrefix-then-Contains
// candidate. It is kept here so benches can compare it with production.
func blockPresentPrefixThenContains(s Selection, messages []model.Message) bool {
	if !s.Selected || s.Block == "" {
		return false
	}
	for i := range messages {
		content := messages[i].Content
		if len(content) < len(s.Block) {
			continue
		}
		if strings.HasPrefix(content, s.Block) || strings.Contains(content, s.Block) {
			return true
		}
	}
	return false
}

func BenchmarkBlockPresent(b *testing.B) {
	block := benchSummaryBlock()
	selection := Selection{Selected: true, Block: block}

	for _, n := range []int{16, 256} {
		for _, loc := range []string{"prefix", "absent"} {
			msgs := benchMessages(n, loc == "prefix", block)
			name := fmt.Sprintf("%s/%d", loc, n)

			b.Run("baseline_noop/"+name, func(b *testing.B) {
				b.ReportAllocs()
				var present bool
				for i := 0; i < b.N; i++ {
					present = blockPresentNoop(selection, msgs)
				}
				if present {
					b.Fatal("noop baseline must not scan")
				}
			})
			b.Run("naive_contains/"+name, func(b *testing.B) {
				b.ReportAllocs()
				want := loc == "prefix"
				var present bool
				for i := 0; i < b.N; i++ {
					present = blockPresentNaive(selection, msgs)
				}
				if present != want {
					b.Fatalf("naive present=%v want=%v", present, want)
				}
			})
			b.Run("prefix_then_contains/"+name, func(b *testing.B) {
				b.ReportAllocs()
				want := loc == "prefix"
				var present bool
				for i := 0; i < b.N; i++ {
					present = blockPresentPrefixThenContains(selection, msgs)
				}
				if present != want {
					b.Fatalf("prefix_then_contains present=%v want=%v", present, want)
				}
			})
			b.Run("current/"+name, func(b *testing.B) {
				b.ReportAllocs()
				want := loc == "prefix"
				var present bool
				for i := 0; i < b.N; i++ {
					present = selection.BlockPresent(msgs)
				}
				if present != want {
					b.Fatalf("current present=%v want=%v", present, want)
				}
			})
		}
	}
}
