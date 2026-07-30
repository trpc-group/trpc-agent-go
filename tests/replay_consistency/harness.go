package replayconsistency

import (
	"context"
	"fmt"
)

func (h ReplayHarness) Run(ctx context.Context) (Report, error) {
	if len(h.Cases) == 0 {
		h.Cases = DefaultReplayCases()
	}
	if h.Options.MaxCases > 0 && h.Options.MaxCases < len(h.Cases) {
		h.Cases = h.Cases[:h.Options.MaxCases]
	}
	var results []CaseResult
	for _, replayCase := range h.Cases {
		select {
		case <-ctx.Done():
			return BuildReport(results), ctx.Err()
		default:
		}
		for _, backend := range h.Backends {
			results = append(results, CaseResult{
				CaseName: replayCase.Name,
				Backend:  backend.Name(),
				Diffs:    []Diff{newDiff(replayCase.Name, backend.Name(), "harness", "baseline", "not implemented", true, "skeleton placeholder; wire a real backend adapter here")},
			})
		}
	}
	return BuildReport(results), nil
}

func ValidateBackendConfig(name string, value string) error {
	if value == "" {
		return fmt.Errorf("%s is not configured", name)
	}
	return nil
}
