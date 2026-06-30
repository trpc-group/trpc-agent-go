package safety

import "testing"

// 测 Scanner.Scan 命中第一条 deny 规则就立即返回
func TestScanner_FirstDenyWins(t *testing.T) {
	scanner := NewScanner(
		NewDangerousCommandRule(),
		NewNetworkAccessRule(),
	)
	// curl 命中 NetworkAccessRule
	res := scanner.Scan(ScanInput{Command: "curl http://evil.com"})
	if res.Decision != DecisionDeny {
		t.Errorf("期望 deny，得到 %s", res.Decision)
	}
}

// 测 Scanner 处理空命令
func TestScanner_EmptyCommand(t *testing.T) {
	scanner := NewScanner(NewDangerousCommandRule())
	res := scanner.Scan(ScanInput{Command: ""})
	if res.Decision != DecisionAllow {
		t.Errorf("空命令应放行，得到 %s", res.Decision)
	}
}
