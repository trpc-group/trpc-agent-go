//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

// jsonWithinDelta reports whether two canonical JSON documents are
// structurally equal with every corresponding pair of numbers within delta
// of each other. Non-numeric values must be exactly equal. Unparsable
// input is never within delta.
func jsonWithinDelta(a, b string, delta float64) bool {
	va, ok := decodeUseNumber(a)
	if !ok {
		return false
	}
	vb, ok := decodeUseNumber(b)
	if !ok {
		return false
	}
	return valuesWithinDelta(va, vb, delta)
}

// decodeUseNumber decodes one JSON document, keeping numbers as json.Number
// so comparison stays exact-decimal.
func decodeUseNumber(s string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

// valuesWithinDelta deep-compares two decoded JSON values: objects and
// arrays recurse, numbers compare within delta, everything else must be
// exactly equal.
func valuesWithinDelta(a, b any, delta float64) bool {
	switch ta := a.(type) {
	case map[string]any:
		tb, ok := b.(map[string]any)
		if !ok || len(ta) != len(tb) {
			return false
		}
		for k, va := range ta {
			vb, ok := tb[k]
			if !ok || !valuesWithinDelta(va, vb, delta) {
				return false
			}
		}
		return true
	case []any:
		tb, ok := b.([]any)
		if !ok || len(ta) != len(tb) {
			return false
		}
		for i := range ta {
			if !valuesWithinDelta(ta[i], tb[i], delta) {
				return false
			}
		}
		return true
	case json.Number:
		nb, ok := b.(json.Number)
		if !ok {
			return false
		}
		return numbersWithinDelta(ta, nb, delta)
	default:
		return reflect.DeepEqual(a, b)
	}
}

// numbersWithinDelta reports whether two numbers differ by at most delta.
// The comparison uses exact decimal arithmetic (big.Rat), so it is free of
// float64 rounding artifacts; inputs with unreasonable magnitude are
// rejected instead of consuming unbounded memory or time.
func numbersWithinDelta(left, right, delta any) bool {
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

// exactNumber converts a numeric value to an exact decimal. NaN and
// infinity are rejected.
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
	// maxExactNumberCharacters bounds the decimal text length so a hostile
	// or corrupt value cannot allocate unbounded big integers.
	maxExactNumberCharacters = 1024
	// maxExactNumberExponent bounds the scientific-notation exponent for
	// the same reason (e.g. "1e1000000000" is rejected, not computed).
	maxExactNumberExponent = 1024
)

// parseBoundedDecimal parses a finite decimal string into an exact
// big.Rat, rejecting over-long or over-scaled input.
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

// decimalPower returns 10^exponent for a non-negative exponent.
func decimalPower(exponent int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
}
