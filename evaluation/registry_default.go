package evaluation

import (
    "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/response"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

// DefaultRegistry 创建一个默认的评估器注册表，对常用指标进行注册。
func DefaultRegistry() *evaluator.Registry {
    r := evaluator.NewRegistry()
    // 文本响应匹配（等价于 ADK response_match_score）。
    _ = r.Register(metric.MetricResponseMatchScore, response.New())
    return r
}
