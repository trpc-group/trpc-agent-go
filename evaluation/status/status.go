package status

type EvalStatus int

const (
	EvalStatusUnknown EvalStatus = iota
	EvalStatusPassed
	EvalStatusFailed
	EvalStatusNotEvaluated
)

func (s EvalStatus) String() string {
	switch s {
	case EvalStatusPassed:
		return "passed"
	case EvalStatusFailed:
		return "failed"
	case EvalStatusNotEvaluated:
		return "not_evaluated"
	default:
		return "unknown"
	}
}
