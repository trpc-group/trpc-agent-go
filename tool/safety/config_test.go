package safety

import (
	"testing"
	"time"
)

// 测 LoadPolicyFile
func TestLoadPolicyFile_Valid(t *testing.T) {
	policy, err := LoadPolicyFile("tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if policy == nil {
		t.Fatal("policy 为 nil")
	}
	if policy.MaxTimeoutSeconds == 0 {
		t.Error("MaxTimeoutSeconds 应有默认值")
	}
}

// 测 LoadPolicyFile 失败场景
func TestLoadPolicyFile_NotFound(t *testing.T) {
	_, err := LoadPolicyFile("不存在的文件.yaml")
	if err == nil {
		t.Error("不存在的文件应返回错误")
	}
}

// 测 NewReport 输入为 nil（无规则触发）
func TestNewReport_Allow(t *testing.T) {
	report := NewReport(nil, ScanInput{Command: "ls"}, "test", time.Millisecond)
	if report.Decision != DecisionAllow {
		t.Errorf("期望 allow，得到 %s", report.Decision)
	}
	if report.Blocked {
		t.Error("allow 不应被拦截")
	}
}

// 测 NewReport 输入为 deny
func TestNewReport_Deny(t *testing.T) {
	res := &ScanResult{
		Decision:  DecisionDeny,
		RiskLevel: RiskCritical,
		RuleID:    "danger_cmd_001",
		Evidence:  "rm -rf /",
		Reason:    "test",
	}
	report := NewReport(res, ScanInput{Command: "rm -rf /"}, "test", time.Millisecond)
	if !report.Blocked {
		t.Error("deny 应被标记为 blocked")
	}
}

// 测 NewReport 输入为 ask
func TestNewReport_Ask(t *testing.T) {
	res := &ScanResult{
		Decision:  DecisionAsk,
		RiskLevel: RiskMedium,
		RuleID:    "ask_review_008",
		Reason:    "test",
	}
	report := NewReport(res, ScanInput{Command: "rm -r"}, "test", time.Millisecond)
	if report.Decision != DecisionAsk {
		t.Error("ask 决策应保留")
	}
}

// 测 NewAuditEvent
func TestNewAuditEvent(t *testing.T) {
	r := ScanReport{
		ToolName:  "exec",
		Command:   "ls",
		Decision:  DecisionAllow,
		RiskLevel: RiskNone,
		Backend:   "local",
		Blocked:   false,
	}
	event := NewAuditEvent(r)
	if event.ToolName != "exec" {
		t.Errorf("事件 ToolName 错误")
	}
	if event.Decision != "allow" {
		t.Errorf("事件 Decision 错误")
	}
}

// 测 SetSpanAttributes
func TestSetSpanAttributes(t *testing.T) {
	r := ScanReport{
		Decision:  DecisionDeny,
		RiskLevel: RiskHigh,
		RuleID:    "network_002",
		Backend:   "local",
		Blocked:   true,
	}
	attrs := SetSpanAttributes(r)
	if attrs[SpanAttrDecision] != "deny" {
		t.Error("decision 属性错误")
	}
	if attrs[SpanAttrBackend] != "local" {
		t.Error("backend 属性错误")
	}
}

// 测 DefaultPolicy
func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	if len(p.DeniedCommands) == 0 {
		t.Error("默认策略应有命令黑名单")
	}
	if p.MaxTimeoutSeconds == 0 {
		t.Error("默认应有超时时间")
	}
}
