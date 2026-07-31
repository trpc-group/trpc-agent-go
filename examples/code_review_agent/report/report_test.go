package report

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

func TestGenerateMarkdownStringIncludesSeverityDistribution(t *testing.T) {
	got := GenerateMarkdownString(&ReviewReport{
		TaskID: "task-1",
		Monitoring: &store.MonitoringSummary{
			SeverityDistribution: map[string]int{
				"low":      1,
				"critical": 2,
				"info":     3,
			},
		},
	})
	for _, want := range []string{"**Severity Distribution:**", "- critical: 2", "- low: 1", "- info: 3"} {
		if !strings.Contains(got, want) {
			t.Errorf("GenerateMarkdownString() missing %q in %q", want, got)
		}
	}
	if strings.Index(got, "- critical: 2") > strings.Index(got, "- low: 1") {
		t.Error("severity distribution is not in stable severity order")
	}
}
