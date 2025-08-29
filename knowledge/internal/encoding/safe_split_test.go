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
				if charCount > tc.size {
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
