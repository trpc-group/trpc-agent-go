// Package attribution provides failure categorization and loss conversion for PromptIter optimization.
package attribution

import (
	"fmt"
	"strings"
)

// Category defines the failure attribution classification.
type Category string

const (
	FinalResponseMismatch       Category = "final_response_mismatch"
	ToolCallError               Category = "tool_call_error"
	ToolArgumentError           Category = "tool_argument_error"
	RouteError                  Category = "route_error"
	FormatError                 Category = "format_error"
	KnowledgeRecallInsufficient Category = "knowledge_recall_insufficient"
	None                        Category = "none"
)

// FailureDetail records an attributed failure item for a test case.
type FailureDetail struct {
	CaseID      string   `json:"case_id"`
	Category    Category `json:"category"`
	Severity    string   `json:"severity"` // "high", "medium", "low"
	Evidence    string   `json:"evidence"`
	Explanation string   `json:"explanation"`
}

// AttributeFailure inspects the response and error string to classify the root cause.
func AttributeFailure(caseID, expectedResp, actualResp, errStr string) FailureDetail {
	if errStr == "" && actualResp == expectedResp {
		return FailureDetail{
			CaseID:      caseID,
			Category:    None,
			Severity:    "info",
			Evidence:    actualResp,
			Explanation: "Case passed successfully without error.",
		}
	}

	lowerErr := strings.ToLower(errStr)
	lowerResp := strings.ToLower(actualResp)

	if strings.Contains(lowerErr, "router") || strings.Contains(lowerResp, "router") || strings.Contains(lowerErr, "route") {
		return FailureDetail{
			CaseID:      caseID,
			Category:    RouteError,
			Severity:    "high",
			Evidence:    errStr,
			Explanation: "Request routed to wrong sub-agent or tool handler.",
		}
	}

	if strings.Contains(lowerErr, "argument") || strings.Contains(lowerResp, "argument") || strings.Contains(lowerErr, "passed string") {
		return FailureDetail{
			CaseID:      caseID,
			Category:    ToolArgumentError,
			Severity:    "high",
			Evidence:    errStr,
			Explanation: "Tool argument type mismatch or invalid parameter format.",
		}
	}

	if strings.Contains(lowerErr, "tool") {
		return FailureDetail{
			CaseID:      caseID,
			Category:    ToolCallError,
			Severity:    "high",
			Evidence:    errStr,
			Explanation: "Tool execution failed or tool call rejected.",
		}
	}

	if strings.Contains(lowerResp, "hedges") || strings.Contains(lowerResp, "not sure") || strings.Contains(lowerResp, "around") {
		return FailureDetail{
			CaseID:      caseID,
			Category:    KnowledgeRecallInsufficient,
			Severity:    "medium",
			Evidence:    actualResp,
			Explanation: "Model produced uncertain or incomplete factual answer.",
		}
	}

	if strings.Contains(lowerErr, "format") || strings.Contains(lowerErr, "json") {
		return FailureDetail{
			CaseID:      caseID,
			Category:    FormatError,
			Severity:    "medium",
			Evidence:    errStr,
			Explanation: "Output format does not conform to requested schema.",
		}
	}

	return FailureDetail{
		CaseID:      caseID,
		Category:    FinalResponseMismatch,
		Severity:    "medium",
		Evidence:    fmt.Sprintf("expected '%s', got '%s'", expectedResp, actualResp),
		Explanation: "Final assistant text response did not match target expectation.",
	}
}
