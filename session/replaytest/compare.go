//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	pathpkg "path"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Compare returns every semantic difference between two normalized snapshots.
func Compare(caseName string, baseline, actual Snapshot, allowed []AllowedDiff) ([]Diff, error) {
	if caseName == "" {
		return nil, errors.New("replaytest: comparison case name is required")
	}
	if err := validateSnapshotMetadata(caseName, baseline, actual); err != nil {
		return nil, err
	}
	if err := validateAllowedDiffs(allowed); err != nil {
		return nil, err
	}
	left, err := snapshotValue(baseline)
	if err != nil {
		return nil, fmt.Errorf("encode baseline snapshot: %w", err)
	}
	right, err := snapshotValue(actual)
	if err != nil {
		return nil, fmt.Errorf("encode actual snapshot: %w", err)
	}
	comparator := comparator{
		caseName: caseName,
		baseline: baseline,
		actual:   actual,
		allowed:  allowed,
	}
	comparator.compareNode("", left, true, right, true)
	return comparator.diffs, nil
}

func validateSnapshotMetadata(caseName string, baseline, actual Snapshot) error {
	if baseline.Backend == "" || actual.Backend == "" {
		return errors.New("replaytest: comparison backend names are required")
	}
	if baseline.Backend == actual.Backend {
		return fmt.Errorf("replaytest: comparison backend %q is repeated", baseline.Backend)
	}
	if baseline.Case != caseName || actual.Case != caseName {
		return fmt.Errorf(
			"replaytest: comparison case %q does not match snapshots %q and %q",
			caseName,
			baseline.Case,
			actual.Case,
		)
	}
	return nil
}

type comparator struct {
	caseName string
	baseline Snapshot
	actual   Snapshot
	allowed  []AllowedDiff
	diffs    []Diff
}

func (c *comparator) compareNode(path string, left any, leftExists bool, right any, rightExists bool) {
	if !leftExists || !rightExists {
		c.addDiff(path, left, leftExists, right, rightExists)
		return
	}
	if c.compareMaps(path, left, right) {
		return
	}
	if c.compareArrays(path, left, right) {
		return
	}
	if !reflect.DeepEqual(left, right) {
		c.addDiff(path, left, true, right, true)
	}
}

func (c *comparator) compareMaps(path string, left, right any) bool {
	leftMap, leftIsMap := left.(map[string]any)
	rightMap, rightIsMap := right.(map[string]any)
	if !leftIsMap && !rightIsMap {
		return false
	}
	if !leftIsMap || !rightIsMap {
		c.addDiff(path, left, true, right, true)
		return true
	}
	keys := make(map[string]struct{}, len(leftMap)+len(rightMap))
	for key := range leftMap {
		keys[key] = struct{}{}
	}
	for key := range rightMap {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		leftValue, leftExists := leftMap[key]
		rightValue, rightExists := rightMap[key]
		c.compareNode(path+"/"+escapePointer(key), leftValue, leftExists, rightValue, rightExists)
	}
	return true
}

func (c *comparator) compareArrays(path string, left, right any) bool {
	leftArray, leftIsArray := left.([]any)
	rightArray, rightIsArray := right.([]any)
	if !leftIsArray && !rightIsArray {
		return false
	}
	if !leftIsArray || !rightIsArray {
		c.addDiff(path, left, true, right, true)
		return true
	}
	if len(leftArray) != len(rightArray) {
		c.addDiff(path+"/length", len(leftArray), true, len(rightArray), true)
	}
	length := max(len(leftArray), len(rightArray))
	for index := 0; index < length; index++ {
		var leftValue, rightValue any
		leftExists := index < len(leftArray)
		rightExists := index < len(rightArray)
		if leftExists {
			leftValue = leftArray[index]
		}
		if rightExists {
			rightValue = rightArray[index]
		}
		c.compareNode(path+"/"+strconv.Itoa(index), leftValue, leftExists, rightValue, rightExists)
	}
	return true
}

func (c *comparator) addDiff(
	path string,
	left any,
	leftExists bool,
	right any,
	rightExists bool,
) {
	if path == "" {
		path = "/"
	}
	diff := Diff{
		Case:        c.caseName,
		BackendA:    c.baseline.Backend,
		BackendB:    c.actual.Backend,
		SessionID:   c.diffSessionID(),
		Path:        path,
		Baseline:    left,
		Actual:      right,
		Explanation: "semantic values differ",
	}
	c.addLocator(&diff)
	for _, rule := range c.allowed {
		if !backendPairMatches(rule, c.baseline.Backend, c.actual.Backend) {
			continue
		}
		matched, err := pathpkg.Match(rule.Path, path)
		if err != nil || !matched {
			continue
		}
		if allowedByRule(rule, left, leftExists, right, rightExists) {
			diff.Allowed = true
			diff.Explanation = rule.Reason
			break
		}
	}
	c.diffs = append(c.diffs, diff)
}

func (c *comparator) diffSessionID() string {
	if sessionID := stringValue(c.baseline.Session["id"]); sessionID != "" {
		return sessionID
	}
	if sessionID := stringValue(c.actual.Session["id"]); sessionID != "" {
		return sessionID
	}
	return c.caseName
}

func (c *comparator) addLocator(diff *Diff) {
	parts := pointerParts(diff.Path)
	if len(parts) < 2 {
		return
	}
	switch parts[0] {
	case "events":
		if index, err := strconv.Atoi(parts[1]); err == nil {
			diff.EventIndex = &index
		}
	case "summaries":
		diff.SummaryFilterKey = parts[1]
	case "tracks":
		diff.TrackName = parts[1]
	case "memories":
		index, err := strconv.Atoi(parts[1])
		if err != nil {
			return
		}
		if index < len(c.baseline.Memories) {
			diff.MemoryID = stringValue(c.baseline.Memories[index]["id"])
		} else if index < len(c.actual.Memories) {
			diff.MemoryID = stringValue(c.actual.Memories[index]["id"])
		}
	}
}

func snapshotValue(snapshot Snapshot) (map[string]any, error) {
	comparable := CanonicalMap{
		"session":     snapshot.Session,
		"events":      snapshot.Events,
		"event_order": snapshot.EventOrder,
		"state":       snapshot.State,
		"memories":    snapshot.Memories,
		"summaries":   snapshot.Summaries,
		"tracks":      snapshot.Tracks,
	}
	raw, err := json.Marshal(comparable)
	if err != nil {
		return nil, err
	}
	var output map[string]any
	if err := decodeJSON(raw, &output); err != nil {
		return nil, err
	}
	return output, nil
}

func validateAllowedDiffs(rules []AllowedDiff) error {
	for index, rule := range rules {
		if rule.BackendA == "" || rule.BackendB == "" || rule.Path == "" || rule.Reason == "" {
			return fmt.Errorf("allowed_diff %d requires backend_a, backend_b, path, and reason", index)
		}
		if !strings.HasPrefix(rule.Path, "/") {
			return fmt.Errorf("allowed_diff %d path must be a JSON pointer glob", index)
		}
		if _, err := pathpkg.Match(rule.Path, rule.Path); err != nil {
			return fmt.Errorf("allowed_diff %d has invalid path glob: %w", index, err)
		}
		switch rule.Rule {
		case AllowedIgnore, AllowedSameType:
		case AllowedWithinDelta:
			if rule.Delta < 0 || math.IsNaN(rule.Delta) || math.IsInf(rule.Delta, 0) {
				return fmt.Errorf("allowed_diff %d delta must be finite and non-negative", index)
			}
		default:
			return fmt.Errorf("allowed_diff %d has unknown rule %q", index, rule.Rule)
		}
	}
	return nil
}

func allowedByRule(
	rule AllowedDiff,
	left any,
	leftExists bool,
	right any,
	rightExists bool,
) bool {
	switch rule.Rule {
	case AllowedIgnore:
		return true
	case AllowedSameType:
		return leftExists && rightExists && reflect.TypeOf(left) == reflect.TypeOf(right)
	case AllowedWithinDelta:
		return leftExists && rightExists && numbersWithinDelta(left, right, rule.Delta)
	default:
		return false
	}
}

func numbersWithinDelta(left, right any, delta float64) bool {
	leftNumber, leftOK := exactNumber(left)
	rightNumber, rightOK := exactNumber(right)
	deltaNumber, deltaOK := exactNumber(delta)
	if !leftOK || !rightOK || !deltaOK {
		return false
	}
	difference := new(big.Rat).Sub(leftNumber, rightNumber)
	difference.Abs(difference)
	return difference.Cmp(deltaNumber) <= 0
}

func exactNumber(value any) (*big.Rat, bool) {
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, false
		}
		text = strconv.FormatFloat(typed, 'g', -1, 64)
	case float32:
		text = strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case int:
		text = strconv.FormatInt(int64(typed), 10)
	case int64:
		text = strconv.FormatInt(typed, 10)
	case uint:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint64:
		text = strconv.FormatUint(typed, 10)
	default:
		return nil, false
	}
	return parseBoundedDecimal(text)
}

const (
	maxExactNumberCharacters = 1024
	maxExactNumberExponent   = 1024
)

func parseBoundedDecimal(text string) (*big.Rat, bool) {
	if text == "" || len(text) > maxExactNumberCharacters {
		return nil, false
	}
	mantissa := text
	exponent := 0
	if exponentIndex := strings.IndexAny(text, "eE"); exponentIndex >= 0 {
		if strings.IndexAny(text[exponentIndex+1:], "eE") >= 0 {
			return nil, false
		}
		mantissa = text[:exponentIndex]
		parsed, err := strconv.ParseInt(text[exponentIndex+1:], 10, 32)
		if err != nil || parsed < -maxExactNumberExponent || parsed > maxExactNumberExponent {
			return nil, false
		}
		exponent = int(parsed)
	}

	negative := strings.HasPrefix(mantissa, "-")
	if negative {
		mantissa = strings.TrimPrefix(mantissa, "-")
	}
	parts := strings.Split(mantissa, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		return nil, false
	}
	fractionDigits := 0
	digits := parts[0]
	if len(parts) == 2 {
		fractionDigits = len(parts[1])
		digits += parts[1]
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return nil, false
		}
	}
	numerator, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, false
	}
	if negative {
		numerator.Neg(numerator)
	}
	scale := exponent - fractionDigits
	if scale >= 0 {
		numerator.Mul(numerator, decimalPower(scale))
		return new(big.Rat).SetInt(numerator), true
	}
	return new(big.Rat).SetFrac(numerator, decimalPower(-scale)), true
}

func decimalPower(exponent int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
}

func backendMatches(pattern, backend string) bool {
	return pattern == "*" || pattern == backend
}

func backendPairMatches(rule AllowedDiff, backendA, backendB string) bool {
	direct := backendMatches(rule.BackendA, backendA) && backendMatches(rule.BackendB, backendB)
	reverse := backendMatches(rule.BackendA, backendB) && backendMatches(rule.BackendB, backendA)
	return direct || reverse
}

func escapePointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func unescapePointer(value string) string {
	value = strings.ReplaceAll(value, "~1", "/")
	return strings.ReplaceAll(value, "~0", "~")
}

func pointerParts(path string) []string {
	if path == "" || path == "/" {
		return nil
	}
	raw := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index := range raw {
		raw[index] = unescapePointer(raw[index])
	}
	return raw
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

// IsClean reports whether a report contains no blocking differences.
func (r Report) IsClean() bool {
	return r.BlockingDiffs == 0 && r.FailedCases == 0
}

// Validate checks report accounting before it is written or consumed.
func (r Report) Validate() error {
	backendNames, err := validateReportMetadata(r)
	if err != nil {
		return err
	}
	totals, err := validateReportCases(r, backendNames)
	if err != nil {
		return err
	}
	return validateReportTotals(r, totals)
}

type reportTotals struct {
	passed      int
	failed      int
	unsupported int
	blocking    int
	allowed     int
}

func validateReportMetadata(r Report) (map[string]struct{}, error) {
	if r.GeneratedAt.IsZero() {
		return nil, errors.New("replaytest: report generated_at is required")
	}
	if len(r.Backends) < 2 {
		return nil, errors.New("replaytest: report requires at least two backends")
	}
	if r.ComparisonMode != ComparisonReference && r.ComparisonMode != ComparisonConsensus {
		return nil, fmt.Errorf("replaytest: report has unknown comparison mode %q", r.ComparisonMode)
	}
	backendNames := make(map[string]struct{}, len(r.Backends))
	for _, backend := range r.Backends {
		if backend == "" {
			return nil, errors.New("replaytest: report backend name is required")
		}
		if _, exists := backendNames[backend]; exists {
			return nil, fmt.Errorf("replaytest: duplicate report backend %q", backend)
		}
		backendNames[backend] = struct{}{}
	}
	if r.ComparisonMode == ComparisonReference {
		if _, ok := backendNames[r.Reference]; !ok {
			return nil, fmt.Errorf("replaytest: reference backend %q is not in backends", r.Reference)
		}
	} else if r.Reference != "" {
		return nil, errors.New("replaytest: consensus report must not name a reference backend")
	}
	if r.TotalCases != len(r.Cases) {
		return nil, fmt.Errorf("replaytest: total_cases=%d but cases=%d", r.TotalCases, len(r.Cases))
	}
	if r.TotalCases == 0 {
		return nil, errors.New("replaytest: report requires at least one case")
	}
	return backendNames, nil
}

func validateReportCases(r Report, backendNames map[string]struct{}) (reportTotals, error) {
	caseNames := make(map[string]struct{}, len(r.Cases))
	var totals reportTotals
	for _, result := range r.Cases {
		if result.Name == "" {
			return reportTotals{}, errors.New("replaytest: report case name is required")
		}
		if _, exists := caseNames[result.Name]; exists {
			return reportTotals{}, fmt.Errorf("replaytest: duplicate report case %q", result.Name)
		}
		caseNames[result.Name] = struct{}{}
		if err := totals.addStatus(result); err != nil {
			return reportTotals{}, err
		}
		caseBlocking, caseAllowed, err := validateCaseResult(
			r.ComparisonMode,
			r.Reference,
			result,
			backendNames,
		)
		if err != nil {
			return reportTotals{}, err
		}
		totals.blocking += caseBlocking
		totals.allowed += caseAllowed
	}
	return totals, nil
}

func (t *reportTotals) addStatus(result CaseResult) error {
	switch result.Status {
	case StatusPassed:
		t.passed++
	case StatusFailed:
		t.failed++
	case StatusUnsupported:
		t.unsupported++
	default:
		return fmt.Errorf("replaytest: case %q has unknown status %q", result.Name, result.Status)
	}
	return nil
}

func validateCaseResult(
	mode ComparisonMode,
	reference string,
	result CaseResult,
	backendNames map[string]struct{},
) (int, int, error) {
	if result.Duration < 0 {
		return 0, 0, fmt.Errorf("replaytest: case %q has negative duration", result.Name)
	}
	blocking, allowed := countDiffs(result.Diffs)
	hasCapabilityEvidence, err := validateCaseDiffs(result, backendNames)
	if err != nil {
		return 0, 0, err
	}
	if err := validateCaseComparison(mode, reference, result, backendNames); err != nil {
		return 0, 0, err
	}
	expectedStatus := expectedCaseStatus(blocking, hasCapabilityEvidence)
	if result.Status != expectedStatus {
		return 0, 0, fmt.Errorf(
			"replaytest: case %q has status %q, want %q from its evidence",
			result.Name,
			result.Status,
			expectedStatus,
		)
	}
	return blocking, allowed, nil
}

func validateCaseDiffs(result CaseResult, backendNames map[string]struct{}) (bool, error) {
	hasCapabilityEvidence := false
	for index, diff := range result.Diffs {
		if diff.Path == "/execution" && diff.Allowed {
			return false, fmt.Errorf("replaytest: case %q allows an execution failure", result.Name)
		}
		if strings.HasPrefix(diff.Path, "/capabilities/") {
			if _, ok := capabilityFromEvidencePath(diff.Path); !ok {
				return false, fmt.Errorf(
					"replaytest: case %q has unknown capability evidence path %q",
					result.Name,
					diff.Path,
				)
			}
			if !diff.Allowed {
				return false, fmt.Errorf("replaytest: case %q has blocking capability evidence", result.Name)
			}
			baseline, baselineOK := diff.Baseline.(bool)
			actual, actualOK := diff.Actual.(bool)
			if !baselineOK || !actualOK || !baseline || actual {
				return false, fmt.Errorf("replaytest: case %q has malformed capability evidence", result.Name)
			}
			hasCapabilityEvidence = true
		}
		if diff.Case != result.Name || diff.BackendA == "" || diff.BackendB == "" ||
			diff.SessionID == "" || !strings.HasPrefix(diff.Path, "/") {
			return false, fmt.Errorf("replaytest: case %q diff %d has an invalid locator", result.Name, index)
		}
		if _, ok := backendNames[diff.BackendA]; !ok {
			return false, fmt.Errorf("replaytest: case %q diff %d names unknown backend %q", result.Name, index, diff.BackendA)
		}
		if _, ok := backendNames[diff.BackendB]; !ok {
			return false, fmt.Errorf("replaytest: case %q diff %d names unknown backend %q", result.Name, index, diff.BackendB)
		}
		if diff.Allowed && diff.Explanation == "" {
			return false, fmt.Errorf("replaytest: case %q diff %d has no allowed_diff explanation", result.Name, index)
		}
	}
	return hasCapabilityEvidence, nil
}

func capabilityFromEvidencePath(path string) (Capability, bool) {
	const prefix = "/capabilities/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	capability := Capability(strings.TrimPrefix(path, prefix))
	return capability, isKnownCapability(capability)
}

func validateCaseComparison(
	mode ComparisonMode,
	reference string,
	result CaseResult,
	backendNames map[string]struct{},
) error {
	if mode == ComparisonReference {
		if result.Consensus != nil {
			return fmt.Errorf("replaytest: reference case %q contains consensus data", result.Name)
		}
		return validateReferenceDiffs(result, reference)
	}
	if result.Consensus == nil {
		return fmt.Errorf("replaytest: consensus case %q has no consensus data", result.Name)
	}
	return validateConsensusResult(result.Name, *result.Consensus, result.Diffs, backendNames)
}

func validateReferenceDiffs(result CaseResult, reference string) error {
	for index, diff := range result.Diffs {
		if diff.BackendA != reference {
			return fmt.Errorf(
				"replaytest: reference case %q diff %d does not start with reference backend %q",
				result.Name,
				index,
				reference,
			)
		}
		if diff.BackendB != reference {
			continue
		}
		_, capabilityEvidence := capabilityFromEvidencePath(diff.Path)
		if diff.Path != "/execution" && !capabilityEvidence {
			return fmt.Errorf("replaytest: reference case %q diff %d is an invalid self diff", result.Name, index)
		}
	}
	return nil
}

func expectedCaseStatus(blocking int, hasCapabilityEvidence bool) string {
	if blocking > 0 {
		return StatusFailed
	}
	if hasCapabilityEvidence {
		return StatusUnsupported
	}
	return StatusPassed
}

func validateReportTotals(r Report, totals reportTotals) error {
	if totals.passed != r.PassedCases || totals.failed != r.FailedCases || totals.unsupported != r.UnsupportedCases {
		return errors.New("replaytest: case status counters do not add up")
	}
	if totals.blocking != r.BlockingDiffs || totals.allowed != r.AllowedDiffs {
		return errors.New("replaytest: diff counters do not add up")
	}
	return nil
}
