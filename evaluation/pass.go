//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evaluation

import (
	"fmt"
	"math"
)

// PassAtK computes the pass@k metric used in LLM / agent evaluation.
//
// pass@k measures the probability that at least one correct solution
// appears among k independently sampled model outputs.
//
// It is commonly used to evaluate stochastic systems (LLMs / agents)
// where multiple attempts are allowed.
//
// Formally:
//
//	Given:
//	  n = total number of sampled attempts
//	  c = number of successful attempts among n
//	  k = number of attempts we hypothetically select (k <= n)
//
//	pass@k is defined as:
//
//	  pass@k = 1 - C(n-c, k) / C(n, k)
//
//	where C(a, b) is the binomial coefficient ("a choose b").
//
// Interpretation:
//
//	From the n observed runs, imagine randomly selecting k runs
//	without replacement. pass@k is the probability that at least
//	one of those k runs is successful.
//
// This is the unbiased estimator introduced in the Codex / HumanEval
// benchmarks and is now standard in LLM evaluation.
//
// Why this formula:
//
//   - Uses all n samples (not just the first k)
//   - Avoids ordering bias
//   - Provides lower-variance estimates when n > k
//
// # Numerical Method
//
// Directly computing factorials or combinations will overflow for
// realistic n. Therefore this implementation operates in log-space
// using math.Lgamma:
//
//	ln(n!) = Lgamma(n+1)
//
// The formula becomes:
//
//	logP = ln((n-c)!)
//	     + ln((n-k)!)
//	     - ln((n-c-k)!)
//	     - ln(n!)
//
//	pass@k = 1 - exp(logP)
//
// This avoids overflow and maintains numerical stability.
//
// Parameters
//
//	n : total number of sampled runs (must be >= 0)
//	c : number of successful runs (must satisfy 0 <= c <= n)
//	k : target k for pass@k (must satisfy 1 <= k <= n)
//
// Return value
//
//	A float64 in [0, 1] representing pass@k.
//
// Edge cases:
//
//   - If c == 0, returns 0
//   - If n-c < k, returns 1 (impossible to select k failures)
//
// IMPORTANT:
//
//	This function assumes all n samples are independent and identically
//	distributed. In agent evaluations, callers must ensure:
//
//	  - Agent state is reset between runs
//	  - No memory/tool cache leakage
//	  - Each run is a fresh stochastic sample
//
// Otherwise pass@k will be systematically overestimated.
func PassAtK(n, c, k int) (float64, error) {
	if n < 0 {
		return 0.0, fmt.Errorf("n must be >= 0")
	}
	if k <= 0 {
		return 0.0, fmt.Errorf("k must be >= 1")
	}
	if c < 0 {
		return 0.0, fmt.Errorf("c must be >= 0")
	}
	if c > n {
		return 0.0, fmt.Errorf("c cannot exceed n")
	}
	if k > n {
		return 0.0, fmt.Errorf("k cannot exceed n")
	}
	// No successes observed.
	if c == 0 {
		return 0.0, nil
	}
	// Fewer than k failures exist -> at least one success guaranteed.
	if n-c < k {
		return 1.0, nil
	}
	nf := float64(n)
	cf := float64(c)
	kf := float64(k)
	// log((n-c)!)
	a, _ := math.Lgamma(nf - cf + 1)
	// log((n-k)!)
	b, _ := math.Lgamma(nf - kf + 1)
	// log((n-c-k)!)
	d, _ := math.Lgamma(nf - cf - kf + 1)
	// log(n!)
	e, _ := math.Lgamma(nf + 1)
	// log probability of drawing k failures
	logP := a + b - d - e
	// pass@k = 1 - exp(logP)
	//
	// Use Expm1 for better precision when logP is close to zero:
	//   1 - exp(x) == -expm1(x)
	return -math.Expm1(logP), nil
}

// PassHatK computes the pass^k metric used in LLM / agent reliability evaluation.
//
// # Concept
//
// pass^k estimates the probability that
// a system succeeds k times in a row, assuming each run is an independent
// Bernoulli trial with identical success probability.
//
// Given:
//
//	n = total number of sampled runs
//	c = number of successful runs among n
//	k = number of consecutive successes required
//
// We first estimate the single-run success probability:
//
//	p = c / n
//
// Then:
//
//	pass^k = p^k
//
// Interpretation:
//
//	pass^k answers:
//
//	  "If I run this system k times independently, what is the probability
//	   that all k runs succeed?"
//
// This metric emphasizes reliability and consistency, in contrast to pass@k,
// which measures whether at least one success appears.
//
// Typical usage:
//
//   - pass@k  : measures peak capability
//   - pass^k  : measures stability / robustness
//
// Statistical Assumptions
//
//   - Each run is independent
//   - Each run follows the same success distribution
//
// In agent systems this requires:
//
//   - Full reset between runs
//   - No memory leakage
//   - No tool cache reuse
//
// Otherwise pass^k will be overestimated.
//
// # Numerical Notes
//
// This implementation uses log-space:
//
//	pass^k = exp(k * log(p))
//
// instead of:
//
//	pow(p, k)
//
// which improves stability when p is very small or k is large.
//
// Parameters
//
//	n : total number of sampled runs (must be > 0)
//	c : number of successful runs (must satisfy 0 <= c <= n)
//	k : number of consecutive successes required (must be >= 1)
//
// Return value
//
//	A float64 in [0, 1] representing pass^k.
//
// Edge cases:
//
//   - If c == 0, returns 0
//   - If c == n, returns 1
func PassHatK(n, c, k int) (float64, error) {
	if n <= 0 {
		return 0.0, fmt.Errorf("n must be > 0")
	}
	if k <= 0 {
		return 0.0, fmt.Errorf("k must be >= 1")
	}
	if c < 0 {
		return 0.0, fmt.Errorf("c must be >= 0")
	}
	if c > n {
		return 0.0, fmt.Errorf("c cannot exceed n")
	}

	// No successes observed.
	if c == 0 {
		return 0.0, nil
	}
	// All runs successful.
	if c == n {
		return 1.0, nil
	}
	p := float64(c) / float64(n)
	// Compute p^k in log-space for numerical stability: p^k = exp(k * log(p))
	return math.Exp(float64(k) * math.Log(p)), nil
}

// ParsePassNC extracts (n, c) from an EvaluationResult for pass@k / pass^k calculations.
func ParsePassNC(result *EvaluationResult) (n, c int, err error) {
	if result == nil {
		return 0, 0, fmt.Errorf("evaluation result is nil")
	}
	if result.EvalResult == nil {
		return 0, 0, fmt.Errorf("eval set result is nil")
	}
	if result.EvalResult.Summary == nil {
		return 0, 0, fmt.Errorf("eval set result summary is nil")
	}
	if result.EvalResult.Summary.RunStatusCounts == nil {
		return 0, 0, fmt.Errorf("run status counts is nil")
	}
	return result.EvalResult.Summary.NumRuns, result.EvalResult.Summary.RunStatusCounts.Passed, nil
}
