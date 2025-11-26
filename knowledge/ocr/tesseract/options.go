//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package tesseract provides Tesseract OCR engine implementation.
package tesseract

// options holds internal configuration for Tesseract OCR Extractor.
type options struct {
	language            string  // e.g., "eng", "chi_sim", "eng+chi_sim"
	confidenceThreshold float64 // 0-100, minimum confidence to accept results
	pageSegMode         int     // Tesseract page segmentation mode (0-13)
}

// Option configures the Tesseract OCR Extractor.
type Option func(*options)

// WithLanguage sets the OCR language(s).
// Use "+" to combine multiple languages, e.g., "eng+chi_sim" for English and Simplified Chinese.
func WithLanguage(lang string) Option {
	return func(c *options) {
		c.language = lang
	}
}

// WithConfidenceThreshold sets the minimum confidence threshold (0-100).
// Results below this threshold will be rejected.
func WithConfidenceThreshold(threshold float64) Option {
	return func(c *options) {
		c.confidenceThreshold = threshold
	}
}

// WithPageSegMode sets the Tesseract page segmentation mode (0-13).
// Common modes:
//
//	0 = Orientation and script detection (OSD) only
//	1 = Automatic page segmentation with OSD
//	3 = Fully automatic page segmentation (default)
//	6 = Uniform block of text
//	7 = Treat the image as a single text line
//	11 = Sparse text. Find as much text as possible in no particular order
//
// Invalid modes (< 0 or > 13) will be ignored and default mode (3) will be used.
func WithPageSegMode(mode int) Option {
	return func(c *options) {
		if mode < 0 || mode > 13 {
			// Keep default mode (3) for invalid values
			return
		}
		c.pageSegMode = mode
	}
}
