//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	retrievalCriticalValuePattern = regexp.MustCompile(
		`(?i)\b(?:[0-9]+(?:[.:/-][0-9]+)*|(?:twenty|thirty|forty|fifty|sixty|seventy|eighty|ninety)(?:[ -]+(?:one|two|three|four|five|six|seven|eight|nine))?|zero|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen)\b|(?:\bnot\b|\bno\b|\bnever\b|\bwithout\b|n't|不再|不是|没有|从未|未|无)`,
	)
	retrievalNumberValues = map[string]int{
		"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4,
		"five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9,
		"ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13,
		"fourteen": 14, "fifteen": 15, "sixteen": 16,
		"seventeen": 17, "eighteen": 18, "nineteen": 19,
		"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50,
		"sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90,
	}
)

func normalizeCriticalValue(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	words := strings.Fields(strings.ReplaceAll(normalized, "-", " "))
	if len(words) == 0 || len(words) > 2 {
		return normalized
	}
	number, ok := retrievalNumberValues[words[0]]
	if !ok {
		return normalized
	}
	if len(words) == 2 {
		unit, ok := retrievalNumberValues[words[1]]
		if !ok || number < 20 || number%10 != 0 || unit < 1 || unit > 9 {
			return normalized
		}
		number += unit
	}
	return strconv.Itoa(number)
}
