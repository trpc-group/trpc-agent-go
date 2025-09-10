package local

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

// Service 是 EvaluationService 的本地实现骨架。
// 目前仅提供最小可用的通道与返回值，便于后续逐步填充并发与实际推理/评估逻辑。
type Service struct{}

// New 返回本地评估服务实例。
func New() *Service { return &Service{} }

// PerformInference 返回一个只读通道；实际实现应启动工作池并发执行推理，
// 将每个用例的 InferenceResult 通过通道流式返回。
func (s *Service) PerformInference(ctx context.Context, req *service.InferenceRequest) (<-chan *service.InferenceResult, error) {
    ch := make(chan *service.InferenceResult)
    // 骨架：直接关闭通道。后续在此处启动 goroutine 推理并写入 ch。
    close(ch)
    return ch, nil
}

// Evaluate 返回一个只读通道；实际实现应依据 EvaluateConfig 中的并发度，
// 拉起评估任务，对每条 InferenceResult 计算各指标，流式返回 EvalCaseResult。
func (s *Service) Evaluate(ctx context.Context, req *service.EvaluateRequest) (<-chan *evalresult.EvalCaseResult, error) {
    ch := make(chan *evalresult.EvalCaseResult)
    // 骨架：直接关闭通道。后续在此处启动 goroutine 评估并写入 ch。
    close(ch)
    return ch, nil
}

// 确保 Service 实现了 service.EvaluationService 接口。
var _ service.EvaluationService = (*Service)(nil)
