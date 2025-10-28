//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package encoding

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSafeSplit tests the SafeSplit function with various UTF-8 scenarios.
func TestSafeSplit(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		pos      int
		expected [2]string
	}{
		{
			name:     "empty string",
			text:     "",
			pos:      5,
			expected: [2]string{"", ""},
		},
		{
			name:     "ASCII text",
			text:     "Hello World",
			pos:      5,
			expected: [2]string{"Hello", " World"},
		},
		{
			name:     "Chinese text",
			text:     "人工智能",
			pos:      2,
			expected: [2]string{"人工", "智能"},
		},
		{
			name:     "mixed text",
			text:     "AI人工智能",
			pos:      2,
			expected: [2]string{"AI", "人工智能"},
		},
		{
			name:     "position at UTF-8 boundary",
			text:     "测试文本",
			pos:      3,
			expected: [2]string{"测试文", "本"},
		},
		{
			name:     "position in middle of UTF-8 character",
			text:     "测试文本",
			pos:      1,
			expected: [2]string{"测", "试文本"},
		},
		{
			name:     "position at zero",
			text:     "Hello",
			pos:      0,
			expected: [2]string{"", "Hello"},
		},
		{
			name:     "negative position",
			text:     "Hello",
			pos:      -1,
			expected: [2]string{"", "Hello"},
		},
		{
			name:     "position beyond text",
			text:     "Hello",
			pos:      100,
			expected: [2]string{"Hello", ""},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			left, right := SafeSplit(tc.text, tc.pos)
			result := [2]string{left, right}

			if result != tc.expected {
				t.Errorf("SafeSplit(%q, %d) = %v, expected %v",
					tc.text, tc.pos, result, tc.expected)
			}

			// Verify that both parts are valid UTF-8.
			if !utf8.ValidString(left) {
				t.Errorf("left part contains invalid UTF-8: %q", left)
			}
			if !utf8.ValidString(right) {
				t.Errorf("right part contains invalid UTF-8: %q", right)
			}
		})
	}
}

// TestSafeSplitBySize tests the SafeSplitBySize function.
func TestSafeSplitBySize(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		size     int
		expected []string
	}{
		{
			name:     "empty text",
			text:     "",
			size:     5,
			expected: []string{""},
		},
		{
			name:     "text smaller than size",
			text:     "Hello",
			size:     10,
			expected: []string{"Hello"},
		},
		{
			name:     "exact size",
			text:     "HelloWorld",
			size:     10,
			expected: []string{"HelloWorld"},
		},
		{
			name:     "multiple chunks",
			text:     "HelloWorld",
			size:     5,
			expected: []string{"Hello", "World"},
		},
		{
			name:     "Chinese text chunks",
			text:     "人工智能机器学习",
			size:     2,
			expected: []string{"人工", "智能", "机器", "学习"},
		},
		{
			name:     "mixed text chunks",
			text:     "AI人工智能",
			size:     2,
			expected: []string{"AI", "人工", "智能"},
		},
		{
			name:     "size zero",
			text:     "Hello",
			size:     0,
			expected: []string{"Hello"},
		},
		{
			name:     "negative size",
			text:     "Hello",
			size:     -1,
			expected: []string{"Hello"},
		},
		{
			name:     "size one",
			text:     "ABC",
			size:     1,
			expected: []string{"A", "B", "C"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := SafeSplitBySize(tc.text, tc.size)

			if len(result) != len(tc.expected) {
				t.Errorf("SafeSplitBySize(%q, %d) returned %d chunks, expected %d",
					tc.text, tc.size, len(result), len(tc.expected))
				return
			}

			for i, chunk := range result {
				if chunk != tc.expected[i] {
					t.Errorf("chunk %d: got %q, expected %q", i, chunk, tc.expected[i])
				}

				// Verify each chunk is valid UTF-8.
				if !utf8.ValidString(chunk) {
					t.Errorf("chunk %d contains invalid UTF-8: %q", i, chunk)
				}

				// Verify chunk size is within limits.
				charCount := utf8.RuneCountInString(chunk)
				if tc.size > 0 && charCount > tc.size {
					t.Errorf("chunk %d exceeds size limit: %d > %d", i, charCount, tc.size)
				}
			}
		})
	}
}

// TestSafeSplitBySeparator tests the SafeSplitBySeparator function.
func TestSafeSplitBySeparator(t *testing.T) {
	testCases := []struct {
		name      string
		text      string
		separator string
		expected  []string
	}{
		{
			name:      "empty separator (character split)",
			text:      "人工智能",
			separator: "",
			expected:  []string{"人", "工", "智", "能"},
		},
		{
			name:      "space separator",
			text:      "AI 人工智能",
			separator: " ",
			expected:  []string{"AI", "人工智能"},
		},
		{
			name:      "Chinese punctuation",
			text:      "人工智能。机器学习，深度学习。",
			separator: "。",
			expected:  []string{"人工智能", "机器学习，深度学习", ""},
		},
		{
			name:      "multi-character separator",
			text:      "Hello,World,Test",
			separator: ",",
			expected:  []string{"Hello", "World", "Test"},
		},
		{
			name:      "no separator in text",
			text:      "HelloWorld",
			separator: ",",
			expected:  []string{"HelloWorld"},
		},
		{
			name:      "empty text",
			text:      "",
			separator: ",",
			expected:  []string{""},
		},
		{
			name:      "separator at start",
			text:      ",HelloWorld",
			separator: ",",
			expected:  []string{"", "HelloWorld"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := SafeSplitBySeparator(tc.text, tc.separator)

			if len(result) != len(tc.expected) {
				t.Errorf("SafeSplitBySeparator(%q, %q) returned %d parts, expected %d",
					tc.text, tc.separator, len(result), len(tc.expected))
				return
			}

			for i, part := range result {
				if part != tc.expected[i] {
					t.Errorf("part %d: got %q, expected %q", i, part, tc.expected[i])
				}

				// Verify each part is valid UTF-8.
				if !utf8.ValidString(part) {
					t.Errorf("part %d contains invalid UTF-8: %q", i, part)
				}
			}
		})
	}
}

// TestSafeSubstring tests the SafeSubstring function.
func TestSafeSubstring(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		start    int
		end      int
		expected string
	}{
		{
			name:     "ASCII substring",
			text:     "Hello World",
			start:    0,
			end:      5,
			expected: "Hello",
		},
		{
			name:     "Chinese substring",
			text:     "人工智能机器学习",
			start:    2,
			end:      4,
			expected: "智能",
		},
		{
			name:     "mixed text substring",
			text:     "AI人工智能",
			start:    0,
			end:      3,
			expected: "AI人",
		},
		{
			name:     "invalid range",
			text:     "Hello",
			start:    5,
			end:      3,
			expected: "",
		},
		{
			name:     "negative start",
			text:     "Hello",
			start:    -1,
			end:      3,
			expected: "",
		},
		{
			name:     "start equals end",
			text:     "Hello",
			start:    2,
			end:      2,
			expected: "",
		},
		{
			name:     "full range",
			text:     "Hello",
			start:    0,
			end:      5,
			expected: "Hello",
		},
		{
			name:     "end beyond text",
			text:     "Hello",
			start:    1,
			end:      100,
			expected: "ello",
		},
		{
			name:     "start at text length",
			text:     "Hello",
			start:    5,
			end:      10,
			expected: "",
		},
		{
			name:     "Chinese substring full",
			text:     "中文",
			start:    0,
			end:      2,
			expected: "中文",
		},
		{
			name:     "single character",
			text:     "中",
			start:    0,
			end:      1,
			expected: "中",
		},
		{
			name:     "middle of Chinese",
			text:     "中国人",
			start:    1,
			end:      2,
			expected: "国",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := SafeSubstring(tc.text, tc.start, tc.end)

			if result != tc.expected {
				t.Errorf("SafeSubstring(%q, %d, %d) = %q, expected %q",
					tc.text, tc.start, tc.end, result, tc.expected)
			}

			// Verify result is valid UTF-8.
			if result != "" && !utf8.ValidString(result) {
				t.Errorf("result contains invalid UTF-8: %q", result)
			}
		})
	}
}

// TestSafeOverlap tests the SafeOverlap function.
func TestSafeOverlap(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		n        int
		expected string
	}{
		{
			name:     "ASCII overlap",
			text:     "Hello World",
			n:        5,
			expected: "World",
		},
		{
			name:     "Chinese overlap",
			text:     "人工智能机器学习",
			n:        2,
			expected: "学习",
		},
		{
			name:     "overlap larger than text",
			text:     "Hello",
			n:        10,
			expected: "Hello",
		},
		{
			name:     "zero overlap",
			text:     "Hello World",
			n:        0,
			expected: "",
		},
		{
			name:     "negative overlap",
			text:     "Hello World",
			n:        -5,
			expected: "",
		},
		{
			name:     "overlap equals text length",
			text:     "Hello",
			n:        5,
			expected: "Hello",
		},
		{
			name:     "single character overlap",
			text:     "Hello World",
			n:        1,
			expected: "d",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := SafeOverlap(tc.text, tc.n)

			if result != tc.expected {
				t.Errorf("SafeOverlap(%q, %d) = %q, expected %q",
					tc.text, tc.n, result, tc.expected)
			}

			// Verify result is valid UTF-8.
			if result != "" && !utf8.ValidString(result) {
				t.Errorf("result contains invalid UTF-8: %q", result)
			}
		})
	}
}

// TestValidateUTF8 tests the ValidateUTF8 function.
func TestValidateUTF8(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		expected string
		valid    bool
	}{
		{
			name:     "valid UTF-8",
			text:     "Hello 世界",
			expected: "Hello 世界",
			valid:    true,
		},
		{
			name:     "invalid UTF-8",
			text:     "Hello" + string([]byte{0xFF, 0xFE}) + "World",
			expected: "HelloWorld",
			valid:    false,
		},
		{
			name:     "empty string",
			text:     "",
			expected: "",
			valid:    true,
		},
		{
			name:     "multiple invalid bytes",
			text:     string([]byte{0xFF, 0xFF, 0xFE}),
			expected: "",
			valid:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateUTF8(tc.text)

			if result != tc.expected {
				t.Errorf("ValidateUTF8(%q) = %q, expected %q",
					tc.text, result, tc.expected)
			}

			// Verify result is valid UTF-8.
			if !utf8.ValidString(result) {
				t.Errorf("result is not valid UTF-8: %q", result)
			}

			// Verify IsValidUTF8 function.
			if IsValidUTF8(tc.text) != tc.valid {
				t.Errorf("IsValidUTF8(%q) = %v, expected %v",
					tc.text, IsValidUTF8(tc.text), tc.valid)
			}
		})
	}
}

// TestRuneCount tests the RuneCount function.
func TestRuneCount(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		expected int
	}{
		{
			name:     "ASCII text",
			text:     "Hello",
			expected: 5,
		},
		{
			name:     "Chinese text",
			text:     "人工智能",
			expected: 4,
		},
		{
			name:     "mixed text",
			text:     "AI人工智能",
			expected: 6,
		},
		{
			name:     "empty string",
			text:     "",
			expected: 0,
		},
		{
			name:     "emoji characters",
			text:     "😀😁😂",
			expected: 3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := RuneCount(tc.text)

			if result != tc.expected {
				t.Errorf("RuneCount(%q) = %d, expected %d",
					tc.text, result, tc.expected)
			}

			// Verify against standard library.
			expected := utf8.RuneCountInString(tc.text)
			if result != expected {
				t.Errorf("RuneCount(%q) = %d, but utf8.RuneCountInString = %d",
					tc.text, result, expected)
			}
		})
	}
}

// TestCharToBytePos tests the charToBytePos helper function.
func TestCharToBytePos(t *testing.T) {
	testCases := []struct {
		name        string
		text        string
		charPos     int
		expectedMin int
		expectedMax int
	}{
		{
			name:        "ASCII at position 0",
			text:        "Hello",
			charPos:     0,
			expectedMin: 0,
			expectedMax: 0,
		},
		{
			name:        "ASCII at position 5",
			text:        "Hello",
			charPos:     5,
			expectedMin: 5,
			expectedMax: 5,
		},
		{
			name:        "Chinese at position 0",
			text:        "人工智能",
			charPos:     0,
			expectedMin: 0,
			expectedMax: 0,
		},
		{
			name:        "Chinese at position 2",
			text:        "人工智能",
			charPos:     2,
			expectedMin: 6,
			expectedMax: 6,
		},
		{
			name:        "negative position",
			text:        "Hello",
			charPos:     -1,
			expectedMin: -1,
			expectedMax: -1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := charToBytePos(tc.text, tc.charPos)

			if result < tc.expectedMin || result > tc.expectedMax {
				t.Errorf("charToBytePos(%q, %d) = %d, expected in range [%d, %d]",
					tc.text, tc.charPos, result, tc.expectedMin, tc.expectedMax)
			}
		})
	}
}

// TestIsValidUTF8Boundary tests the isValidUTF8Boundary helper function.
func TestIsValidUTF8Boundary(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		pos      int
		expected bool
	}{
		{
			name:     "position 0",
			text:     "Hello",
			pos:      0,
			expected: true,
		},
		{
			name:     "ASCII character boundary",
			text:     "Hello",
			pos:      1,
			expected: true,
		},
		{
			name:     "text length",
			text:     "Hello",
			pos:      5,
			expected: true,
		},
		{
			name:     "negative position",
			text:     "Hello",
			pos:      -1,
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isValidUTF8Boundary(tc.text, tc.pos)

			if result != tc.expected {
				t.Errorf("isValidUTF8Boundary(%q, %d) = %v, expected %v",
					tc.text, tc.pos, result, tc.expected)
			}
		})
	}
}

// TestFindSafeSplitPoint tests the findSafeSplitPoint helper function.
func TestFindSafeSplitPoint(t *testing.T) {
	testCases := []struct {
		name            string
		text            string
		targetPos       int
		expectedAtLeast int
		expectedAtMost  int
	}{
		{
			name:            "position 0",
			text:            "Hello",
			targetPos:       0,
			expectedAtLeast: 0,
			expectedAtMost:  0,
		},
		{
			name:            "position in middle",
			text:            "Hello",
			targetPos:       2,
			expectedAtLeast: 0,
			expectedAtMost:  5,
		},
		{
			name:            "position at end",
			text:            "Hello",
			targetPos:       5,
			expectedAtLeast: 5,
			expectedAtMost:  5,
		},
		{
			name:            "position beyond end",
			text:            "Hello",
			targetPos:       100,
			expectedAtLeast: 5,
			expectedAtMost:  5,
		},
		{
			name:            "Chinese text",
			text:            "人工智能",
			targetPos:       3,
			expectedAtLeast: 0,
			expectedAtMost:  12,
		},
		{
			name:            "single byte position",
			text:            "A",
			targetPos:       1,
			expectedAtLeast: 1,
			expectedAtMost:  1,
		},
		{
			name:            "multibyte position",
			text:            "中",
			targetPos:       1,
			expectedAtLeast: 0,
			expectedAtMost:  3,
		},
		{
			name:            "large position",
			text:            "Hello",
			targetPos:       1000,
			expectedAtLeast: 5,
			expectedAtMost:  5,
		},
		{
			name:            "empty string",
			text:            "",
			targetPos:       5,
			expectedAtLeast: 0,
			expectedAtMost:  0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := findSafeSplitPoint(tc.text, tc.targetPos)

			if result < tc.expectedAtLeast || result > tc.expectedAtMost {
				t.Errorf("findSafeSplitPoint(%q, %d) = %d, expected in range [%d, %d]",
					tc.text, tc.targetPos, result, tc.expectedAtLeast, tc.expectedAtMost)
			}

			// Verify the result is a valid split point
			if result > 0 && result < len(tc.text) {
				if !isValidUTF8Boundary(tc.text, result) {
					t.Errorf("result %d is not a valid UTF-8 boundary in %q", result, tc.text)
				}
			}
		})
	}
}

// TestSplitByRunes tests the splitByRunes helper function.
func TestSplitByRunes(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		expected []string
	}{
		{
			name:     "ASCII text",
			text:     "ABC",
			expected: []string{"A", "B", "C"},
		},
		{
			name:     "Chinese text",
			text:     "人工智",
			expected: []string{"人", "工", "智"},
		},
		{
			name:     "mixed text",
			text:     "A人B",
			expected: []string{"A", "人", "B"},
		},
		{
			name:     "empty string",
			text:     "",
			expected: []string{},
		},
		{
			name:     "Chinese text with multiple characters",
			text:     "中国人",
			expected: []string{"中", "国", "人"},
		},
		{
			name:     "single character",
			text:     "中",
			expected: []string{"中"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := splitByRunes(tc.text)

			if len(result) != len(tc.expected) {
				t.Errorf("splitByRunes(%q) returned %d runes, expected %d",
					tc.text, len(result), len(tc.expected))
				return
			}

			for i, rune := range result {
				if rune != tc.expected[i] {
					t.Errorf("rune %d: got %q, expected %q", i, rune, tc.expected[i])
				}
			}
		})
	}
}

// BenchmarkSafeSplit benchmarks the SafeSplit function.
func BenchmarkSafeSplit(b *testing.B) {
	text := "人工智能机器学习深度学习神经网络自然语言处理计算机视觉"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SafeSplit(text, len(text)/2)
	}
}

// BenchmarkSafeSplitBySize benchmarks the SafeSplitBySize function.
func BenchmarkSafeSplitBySize(b *testing.B) {
	text := strings.Repeat("人工智能机器学习深度学习神经网络", 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SafeSplitBySize(text, 50)
	}
}

// TestIsValidUTF8 tests the IsValidUTF8 exported function.
func TestIsValidUTF8Exported(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		expected bool
	}{
		{
			name:     "valid UTF-8",
			text:     "Hello 世界",
			expected: true,
		},
		{
			name:     "invalid UTF-8",
			text:     "Hello" + string([]byte{0xFF, 0xFE}) + "World",
			expected: false,
		},
		{
			name:     "empty string",
			text:     "",
			expected: true,
		},
		{
			name:     "Chinese characters",
			text:     "人工智能",
			expected: true,
		},
		{
			name:     "emoji",
			text:     "😀😁😂",
			expected: true,
		},
		{
			name:     "mixed valid UTF-8",
			text:     "Hello世界Test",
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := IsValidUTF8(tc.text)
			if result != tc.expected {
				t.Errorf("IsValidUTF8(%q) = %v, expected %v",
					tc.text, result, tc.expected)
			}
		})
	}
}

// TestSafeSplitEdgeCases tests additional edge cases for SafeSplit.
func TestSafeSplitEdgeCases(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		pos      int
		leftLen  int
		rightLen int
	}{
		{
			name:     "split at each character",
			text:     "ABC",
			pos:      1,
			leftLen:  1,
			rightLen: 2,
		},
		{
			name:     "Chinese characters split",
			text:     "中文测试",
			pos:      2,
			leftLen:  2,
			rightLen: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			left, right := SafeSplit(tc.text, tc.pos)

			if len([]rune(left)) != tc.leftLen {
				t.Errorf("SafeSplit left part length = %d, expected %d",
					len([]rune(left)), tc.leftLen)
			}

			if len([]rune(right)) != tc.rightLen {
				t.Errorf("SafeSplit right part length = %d, expected %d",
					len([]rune(right)), tc.rightLen)
			}

			// Verify UTF-8 validity
			if !utf8.ValidString(left) || !utf8.ValidString(right) {
				t.Errorf("SafeSplit result contains invalid UTF-8")
			}
		})
	}
}

// TestSafeSplitBySizeEdgeCases tests additional edge cases for SafeSplitBySize.
func TestSafeSplitBySizeEdgeCases(t *testing.T) {
	testCases := []struct {
		name          string
		text          string
		size          int
		expectedCount int
	}{
		{
			name:          "very small text",
			text:          "A",
			size:          1,
			expectedCount: 1,
		},
		{
			name:          "size larger than text",
			text:          "AB",
			size:          1000,
			expectedCount: 1,
		},
		{
			name:          "emoji text",
			text:          "😀😁😂😃",
			size:          2,
			expectedCount: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := SafeSplitBySize(tc.text, tc.size)

			if len(result) != tc.expectedCount {
				t.Errorf("SafeSplitBySize returned %d chunks, expected %d",
					len(result), tc.expectedCount)
			}

			// Verify all chunks are valid UTF-8
			for i, chunk := range result {
				if !utf8.ValidString(chunk) {
					t.Errorf("chunk %d contains invalid UTF-8", i)
				}
			}
		})
	}
}

// TestCharToBytePosBoundary tests charToBytePos at character boundaries.
func TestCharToBytePosBoundary(t *testing.T) {
	testCases := []struct {
		name    string
		text    string
		charPos int
		verify  bool
	}{
		{
			name:    "multibyte at boundary 1",
			text:    "中",
			charPos: 1,
			verify:  true,
		},
		{
			name:    "multibyte at boundary 2",
			text:    "中国",
			charPos: 1,
			verify:  true,
		},
		{
			name:    "mixed multibyte",
			text:    "aB中c",
			charPos: 3,
			verify:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := charToBytePos(tc.text, tc.charPos)

			if result < 0 && result != -1 {
				t.Errorf("charToBytePos returned invalid result: %d", result)
			}

			if result >= 0 && result > len(tc.text) {
				t.Errorf("charToBytePos returned %d, but text length is %d",
					result, len(tc.text))
			}
		})
	}
}
