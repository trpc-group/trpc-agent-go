package agent

import (
	"strings"
	"time"
)

type PermissionPolicy struct {
	AllowStaticcheck bool
}

func (p PermissionPolicy) Decide(taskID, tool string, command []string) PermissionDecision {
	joined := strings.ToLower(strings.Join(command, " "))
	decision := PermissionDecision{
		ID:        NewID("perm"),
		TaskID:    taskID,
		Tool:      tool,
		Command:   append([]string(nil), command...),
		Decision:  DecisionAllow,
		Risk:      "low",
		Reason:    "read-only Go check command is allowed by policy",
		CreatedAt: time.Now().UTC(),
	}

	blocked := []string{" rm ", " del ", " remove-item", "curl ", "wget ", "powershell", "cmd.exe", " sh -c", " bash -c", "docker run --privileged", "--privileged", "sudo", "chmod 777"}
	padded := " " + joined + " "
	for _, token := range blocked {
		if strings.Contains(padded, token) {
			decision.Decision = DecisionDeny
			decision.Risk = "critical"
			decision.Reason = "command contains a destructive, network, shell, or privilege escalation pattern"
			return decision
		}
	}
	if strings.Contains(joined, "staticcheck") && !p.AllowStaticcheck {
		decision.Decision = DecisionNeedsHumanReview
		decision.Risk = "medium"
		decision.Reason = "staticcheck is optional and must be explicitly enabled for this workspace"
		return decision
	}
	if strings.Contains(joined, "go test") || strings.Contains(joined, "go vet") {
		decision.Risk = "medium"
		decision.Reason = "Go toolchain command is allowed with timeout, output cap, env allowlist, and redaction"
	}
	return decision
}
