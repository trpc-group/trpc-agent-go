//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package encoding

import (
	"testing"
)

// TestDetectEncoding tests the encoding detection functionality.
func TestDetectEncoding(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		expected Encoding
		valid    bool
	}{
		{
			name:     "empty string",
			text:     "",
			expected: EncodingUTF8,
			valid:    true,
		},
		{
			name:     "pure ASCII",
			text:     "Hello World",
			expected: EncodingUTF8,
			valid:    true,
		},
		{
			name:     "Chinese UTF-8",
			text:     "人工智能机器学习",
			expected: EncodingUTF8,
			valid:    true,
		},
		{
			name:     "mixed UTF-8",
			text:     "Hello 世界 World",
			expected: EncodingUTF8,
			valid:    true,
		},
		{
			name:     "invalid UTF-8",
			text:     "Hello" + string([]byte{0xFF, 0xFE}) + "World",
			expected: EncodingUnknown,
			valid:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			info := DetectEncoding(tc.text)

			if info.Encoding != tc.expected {
				t.Errorf("DetectEncoding(%q) = %s, expected %s",
					tc.text, info.Encoding, tc.expected)
			}

			if info.IsValid != tc.valid {
				t.Errorf("DetectEncoding(%q) valid = %v, expected %v",
					tc.text, info.IsValid, tc.valid)
			}

			// Verify confidence is reasonable
			if info.Confidence < 0.0 || info.Confidence > 1.0 {
				t.Errorf("DetectEncoding(%q) confidence = %f, should be between 0.0 and 1.0",
					tc.text, info.Confidence)
			}
		})
	}
}

// TestSmartProcessText tests the smart text processing functionality.
func TestSmartProcessText(t *testing.T) {
	testCases := []struct {
		name           string
		text           string
		expectUTF8     bool
		expectValid    bool
		expectEncoding Encoding
	}{
		{
			name:           "valid UTF-8",
			text:           "Hello 世界",
			expectUTF8:     true,
			expectValid:    true,
			expectEncoding: EncodingUTF8,
		},
		{
			name:           "empty string",
			text:           "",
			expectUTF8:     true,
			expectValid:    true,
			expectEncoding: EncodingUTF8,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			processed, info := SmartProcessText(tc.text)

			// Verify the processed text is not empty (unless input was empty)
			if tc.text != "" && processed == "" {
				t.Errorf("SmartProcessText(%q) returned empty processed text", tc.text)
			}

			// Verify encoding info
			if info.Encoding != tc.expectEncoding {
				t.Errorf("SmartProcessText(%q) encoding = %s, expected %s",
					tc.text, info.Encoding, tc.expectEncoding)
			}

			if info.IsValid != tc.expectValid {
				t.Errorf("SmartProcessText(%q) valid = %v, expected %v",
					tc.text, info.IsValid, tc.expectValid)
			}

			// Verify the processed text is valid UTF-8
			if !IsValidUTF8(processed) {
				t.Errorf("SmartProcessText(%q) returned invalid UTF-8: %q", tc.text, processed)
			}
		})
	}
}

// TestIsUTF8Safe tests the UTF-8 safety check.
func TestIsUTF8Safe(t *testing.T) {
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
			name:     "empty string",
			text:     "",
			expected: true,
		},
		{
			name:     "pure ASCII",
			text:     "Hello World",
			expected: true,
		},
		{
			name:     "invalid UTF-8",
			text:     "Hello" + string([]byte{0xFF, 0xFE}) + "World",
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := IsUTF8Safe(tc.text)

			if result != tc.expected {
				t.Errorf("IsUTF8Safe(%q) = %v, expected %v",
					tc.text, result, tc.expected)
			}
		})
	}
}

// TestEncodingPatterns tests the encoding pattern detection.
func TestEncodingPatterns(t *testing.T) {
	// Note: These tests use byte patterns that simulate different encodings
	// In real scenarios, you'd need actual encoded text to test properly

	testCases := []struct {
		name     string
		bytes    []byte
		expected Encoding
	}{
		{
			name:     "empty bytes",
			bytes:    []byte{},
			expected: EncodingUnknown,
		},
		{
			name:     "ASCII only",
			bytes:    []byte("Hello World"),
			expected: EncodingUnknown, // Will be detected as UTF-8 by main detection
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test individual pattern detection functions
			if tc.name == "empty bytes" {
				if isLikelyGBK(tc.bytes) {
					t.Error("isLikelyGBK returned true for empty bytes")
				}
				if isLikelyBig5(tc.bytes) {
					t.Error("isLikelyBig5 returned true for empty bytes")
				}
				if isLikelyShiftJIS(tc.bytes) {
					t.Error("isLikelyShiftJIS returned true for empty bytes")
				}
				if isLikelyEUCKR(tc.bytes) {
					t.Error("isLikelyEUCKR returned true for empty bytes")
				}
			}
		})
	}
}

// TestCalculateUTF8Confidence tests the UTF-8 confidence calculation.
func TestCalculateUTF8Confidence(t *testing.T) {
	testCases := []struct {
		name        string
		text        string
		expectedMin float64
		expectedMax float64
	}{
		{
			name:        "pure ASCII",
			text:        "Hello World",
			expectedMin: 0.8,
			expectedMax: 0.9,
		},
		{
			name:        "with multi-byte",
			text:        "Hello 世界",
			expectedMin: 0.9,
			expectedMax: 1.0,
		},
		{
			name:        "with CJK",
			text:        "人工智能",
			expectedMin: 0.9,
			expectedMax: 1.0,
		},
		{
			name:        "invalid UTF-8",
			text:        "Hello" + string([]byte{0xFF, 0xFE}) + "World",
			expectedMin: 0.0,
			expectedMax: 0.0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			confidence := calculateUTF8Confidence(tc.text)

			if confidence < tc.expectedMin || confidence > tc.expectedMax {
				t.Errorf("calculateUTF8Confidence(%q) = %f, expected between %f and %f",
					tc.text, confidence, tc.expectedMin, tc.expectedMax)
			}
		})
	}
}

// BenchmarkDetectEncoding benchmarks the encoding detection performance.
func BenchmarkDetectEncoding(b *testing.B) {
	text := "人工智能机器学习深度学习神经网络自然语言处理计算机视觉"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DetectEncoding(text)
	}
}

// BenchmarkSmartProcessText benchmarks the smart text processing performance.
func BenchmarkSmartProcessText(b *testing.B) {
	text := "人工智能机器学习深度学习神经网络自然语言处理计算机视觉"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SmartProcessText(text)
	}
}
