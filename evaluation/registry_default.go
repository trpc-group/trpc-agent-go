package evaluation

import "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"

// DefaultRegistry 创建一个默认的评估器注册表。
// 初期可以不注册任何评估器，后续在这里注册：
//  - 工具轨迹评估器（tool_trajectory_avg_score）
//  - 最终响应 ROUGE 匹配（response_match_score）
//  - 响应质量 LLM Judge（response_evaluation_score）
//  - 安全性 LLM Judge（safety_v1）
func DefaultRegistry() *evaluator.Registry {
    r := evaluator.NewRegistry()
    // 示例：r.Register(metric.MetricToolTrajectoryAvgScore, NewTrajectoryEvaluator(...))
    return r
}
