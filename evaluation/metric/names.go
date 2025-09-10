package metric

// 统一的预置指标名称常量，避免在入口包重复定义。
const (
    MetricToolTrajectoryAvgScore  = "tool_trajectory_avg_score"
    MetricResponseEvaluationScore = "response_evaluation_score"
    MetricResponseMatchScore      = "response_match_score"
    MetricSafetyV1                = "safety_v1"
    MetricFinalResponseMatchV2    = "final_response_match_v2"
)

