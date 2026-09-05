//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const supportedConfigVersion = 1

var cpuSuffixPattern = regexp.MustCompile(`-\d+$`)

type config struct {
	Version    int         `json:"version"`
	Benchmarks []budget    `json:"benchmarks"`
	Invariants []invariant `json:"invariants"`
}

type budget struct {
	Package        string   `json:"package"`
	Name           string   `json:"name"`
	MaxBytesPerOp  *float64 `json:"max_bytes_per_op,omitempty"`
	MaxAllocsPerOp *float64 `json:"max_allocs_per_op,omitempty"`
}

type invariant struct {
	Name                string   `json:"name"`
	Package             string   `json:"package"`
	Baseline            string   `json:"baseline"`
	Candidate           string   `json:"candidate"`
	MaxBytesDelta       *float64 `json:"max_bytes_delta,omitempty"`
	MaxAllocsDelta      *float64 `json:"max_allocs_delta,omitempty"`
	MaxBytesRatio       *float64 `json:"max_bytes_ratio,omitempty"`
	MaxAllocationsRatio *float64 `json:"max_allocations_ratio,omitempty"`
}

type benchmarkKey struct {
	Package string
	Name    string
}

type measurement struct {
	BytesPerOp  float64
	AllocsPerOp float64
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("benchguard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "Go benchmark output")
	budgetPath := flags.String("budgets", "", "allocation budget JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *inputPath == "" || *budgetPath == "" {
		fmt.Fprintln(stderr, "both -input and -budgets are required")
		return 2
	}

	cfg, err := loadConfig(*budgetPath)
	if err != nil {
		fmt.Fprintf(stderr, "load budgets: %v\n", err)
		return 1
	}
	input, err := os.Open(*inputPath)
	if err != nil {
		fmt.Fprintf(stderr, "open benchmark input: %v\n", err)
		return 1
	}
	defer input.Close()
	measurements, err := parseBenchmarkOutput(input)
	if err != nil {
		fmt.Fprintf(stderr, "parse benchmark input: %v\n", err)
		return 1
	}

	violations := evaluate(cfg, measurements)
	if len(violations) > 0 {
		for _, violation := range violations {
			fmt.Fprintf(stderr, "benchmark allocation guard: %s\n", violation)
		}
		return 1
	}
	fmt.Fprintf(
		stdout,
		"benchmark allocation guard passed (%d budgets, %d invariants)\n",
		len(cfg.Benchmarks),
		len(cfg.Invariants),
	)
	return 0
}

func loadConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, err
	}
	var cfg config
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return config{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return config{}, fmt.Errorf("decode trailing budget config data: %w", err)
		}
		return config{}, errors.New("budget config must contain exactly one JSON value")
	}
	if cfg.Version != supportedConfigVersion {
		return config{}, fmt.Errorf(
			"unsupported config version %d, want %d",
			cfg.Version,
			supportedConfigVersion,
		)
	}
	if err := validateConfig(cfg); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func validateConfig(cfg config) error {
	seen := make(map[benchmarkKey]struct{}, len(cfg.Benchmarks))
	for _, budget := range cfg.Benchmarks {
		key := benchmarkKey{Package: budget.Package, Name: budget.Name}
		if key.Package == "" || key.Name == "" {
			return errors.New("benchmark package and name are required")
		}
		if budget.MaxBytesPerOp == nil && budget.MaxAllocsPerOp == nil {
			return fmt.Errorf("benchmark %s has no limits", formatKey(key))
		}
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate benchmark budget %s", formatKey(key))
		}
		seen[key] = struct{}{}
	}
	for _, invariant := range cfg.Invariants {
		if invariant.Name == "" || invariant.Package == "" ||
			invariant.Baseline == "" || invariant.Candidate == "" {
			return errors.New("invariant name, package, baseline, and candidate are required")
		}
		if invariant.MaxBytesDelta == nil &&
			invariant.MaxAllocsDelta == nil &&
			invariant.MaxBytesRatio == nil &&
			invariant.MaxAllocationsRatio == nil {
			return fmt.Errorf("invariant %q has no limits", invariant.Name)
		}
	}
	return nil
}

func parseBenchmarkOutput(reader io.Reader) (map[benchmarkKey][]measurement, error) {
	results := make(map[benchmarkKey][]measurement)
	currentPackage := ""
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "pkg:") {
			currentPackage = strings.TrimSpace(strings.TrimPrefix(line, "pkg:"))
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasPrefix(fields[0], "Benchmark") {
			continue
		}
		if currentPackage == "" {
			return nil, fmt.Errorf("benchmark %q has no package header", fields[0])
		}
		var sample measurement
		var haveBytes, haveAllocs bool
		for i := 2; i+1 < len(fields); i += 2 {
			value, err := strconv.ParseFloat(fields[i], 64)
			if err != nil {
				continue
			}
			switch fields[i+1] {
			case "B/op":
				sample.BytesPerOp = value
				haveBytes = true
			case "allocs/op":
				sample.AllocsPerOp = value
				haveAllocs = true
			}
		}
		if !haveBytes || !haveAllocs {
			return nil, fmt.Errorf("benchmark %q is missing -benchmem metrics", fields[0])
		}
		key := benchmarkKey{
			Package: currentPackage,
			Name:    cpuSuffixPattern.ReplaceAllString(fields[0], ""),
		}
		results[key] = append(results[key], sample)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func evaluate(cfg config, results map[benchmarkKey][]measurement) []string {
	var violations []string
	for _, budget := range cfg.Benchmarks {
		key := benchmarkKey{Package: budget.Package, Name: budget.Name}
		samples := results[key]
		if len(samples) == 0 {
			violations = append(violations, fmt.Sprintf("missing benchmark %s", formatKey(key)))
			continue
		}
		for _, sample := range samples {
			if budget.MaxBytesPerOp != nil && sample.BytesPerOp > *budget.MaxBytesPerOp {
				violations = append(violations, fmt.Sprintf(
					"%s uses %.0f B/op, limit %.0f",
					formatKey(key), sample.BytesPerOp, *budget.MaxBytesPerOp,
				))
			}
			if budget.MaxAllocsPerOp != nil && sample.AllocsPerOp > *budget.MaxAllocsPerOp {
				violations = append(violations, fmt.Sprintf(
					"%s uses %.0f allocs/op, limit %.0f",
					formatKey(key), sample.AllocsPerOp, *budget.MaxAllocsPerOp,
				))
			}
		}
	}
	for _, invariant := range cfg.Invariants {
		baselineKey := benchmarkKey{Package: invariant.Package, Name: invariant.Baseline}
		candidateKey := benchmarkKey{Package: invariant.Package, Name: invariant.Candidate}
		baseline := results[baselineKey]
		candidate := results[candidateKey]
		if len(baseline) == 0 || len(candidate) == 0 {
			violations = append(violations, fmt.Sprintf(
				"invariant %q is missing baseline or candidate results",
				invariant.Name,
			))
			continue
		}
		baseBytes, baseAllocs := maximumMetrics(baseline)
		candidateBytes, candidateAllocs := maximumMetrics(candidate)
		if invariant.MaxBytesDelta != nil &&
			candidateBytes-baseBytes > *invariant.MaxBytesDelta {
			violations = append(violations, fmt.Sprintf(
				"invariant %q grows by %.0f B/op, limit %.0f",
				invariant.Name,
				candidateBytes-baseBytes,
				*invariant.MaxBytesDelta,
			))
		}
		if invariant.MaxAllocsDelta != nil &&
			candidateAllocs-baseAllocs > *invariant.MaxAllocsDelta {
			violations = append(violations, fmt.Sprintf(
				"invariant %q grows by %.0f allocs/op, limit %.0f",
				invariant.Name,
				candidateAllocs-baseAllocs,
				*invariant.MaxAllocsDelta,
			))
		}
		if invariant.MaxBytesRatio != nil &&
			exceedsRatio(candidateBytes, baseBytes, *invariant.MaxBytesRatio) {
			violations = append(violations, fmt.Sprintf(
				"invariant %q bytes ratio %.2f exceeds %.2f",
				invariant.Name,
				ratio(candidateBytes, baseBytes),
				*invariant.MaxBytesRatio,
			))
		}
		if invariant.MaxAllocationsRatio != nil &&
			exceedsRatio(candidateAllocs, baseAllocs, *invariant.MaxAllocationsRatio) {
			violations = append(violations, fmt.Sprintf(
				"invariant %q allocations ratio %.2f exceeds %.2f",
				invariant.Name,
				ratio(candidateAllocs, baseAllocs),
				*invariant.MaxAllocationsRatio,
			))
		}
	}
	return violations
}

func maximumMetrics(samples []measurement) (float64, float64) {
	var maxBytes, maxAllocs float64
	for _, sample := range samples {
		if sample.BytesPerOp > maxBytes {
			maxBytes = sample.BytesPerOp
		}
		if sample.AllocsPerOp > maxAllocs {
			maxAllocs = sample.AllocsPerOp
		}
	}
	return maxBytes, maxAllocs
}

func exceedsRatio(candidate, baseline, limit float64) bool {
	if baseline == 0 {
		return candidate > 0
	}
	return candidate/baseline > limit
}

func ratio(candidate, baseline float64) float64 {
	if baseline == 0 {
		return candidate
	}
	return candidate / baseline
}

func formatKey(key benchmarkKey) string {
	return key.Package + ":" + key.Name
}
