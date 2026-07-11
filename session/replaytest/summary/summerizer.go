package summary

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

//模拟一个 summarizer  代替大模型

// FakeSummarizer 用确定性规则模拟大模型摘要，避免测试依赖真实 LLM。
type FakeSummarizer struct{}

func (FakeSummarizer) ShouldSummarize(sess *session.Session) bool {
	return true
}

// 按 session ID 和事件内容拼接摘要文本。
func (FakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	// 组装文本
	var b strings.Builder

	fmt.Fprintf(&b, "summary:%s:", sess.ID)
	for _, evt := range sess.Events {
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		msg := evt.Response.Choices[0].Message
		fmt.Fprintf(&b, "[%s]%s;", msg.Role, msg.Content)
	}

	return b.String(), nil
}

// 实现空方法  满足 withSummarizer()要求
func (FakeSummarizer) SetPrompt(prompt string) {}

func (FakeSummarizer) SetModel(m model.Model) {}

func (FakeSummarizer) Metadata() map[string]any {
	return nil
}
