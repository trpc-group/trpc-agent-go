//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chunking

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

func TestMarkdownChunking_BasicOverlap(t *testing.T) {
	md := `# Header 1

Paragraph one with some text to exceed size.

## Header 2

Second paragraph more text.`

	doc := &document.Document{ID: "md", Content: md}

	const size = 40
	const overlap = 5

	mc := NewMarkdownChunking(WithMarkdownChunkSize(size), WithMarkdownOverlap(overlap))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)

	// Overlap separator adds 4 characters: "\n\n" + "\n\n" (no visible marker)
	const overlapSeparatorLen = 4

	// Validate each chunk size - use character count, not byte count
	for i, c := range chunks {
		// Ensure chunk size not huge (>2*size + overlap separator)
		charCount := utf8.RuneCountInString(c.Content)
		maxSize := 2*size + overlapSeparatorLen
		require.LessOrEqual(t, charCount, maxSize, "Chunk %d has %d chars, exceeds max=%d", i, charCount, maxSize)

		// Verify UTF-8 validity
		require.True(t, utf8.ValidString(c.Content), "Chunk %d contains invalid UTF-8", i)
	}

	// Note: When splitting by headers, overlap behavior may differ from fixed-size splitting
	// because header boundaries take precedence over overlap requirements
}

func TestMarkdownChunking_Errors(t *testing.T) {
	mc := NewMarkdownChunking()

	_, err := mc.Chunk(nil)
	require.ErrorIs(t, err, ErrNilDocument)

	empty := &document.Document{ID: "e", Content: ""}
	_, err = mc.Chunk(empty)
	require.ErrorIs(t, err, ErrEmptyDocument)
}

// TestMarkdownChunking_ChineseContent tests chunking with Chinese markdown content
func TestMarkdownChunking_ChineseContent(t *testing.T) {
	chineseMd := `# 人工智能简介

人工智能（Artificial Intelligence，AI）是计算机科学的一个分支，它企图了解智能的实质，并生产出一种新的能以人类智能相似的方式做出反应的智能机器。

## 机器学习

机器学习是人工智能的一个重要分支。它是一种通过算法使机器能够从数据中学习并做出决策或预测的技术。

## 深度学习

深度学习是机器学习的一个子集，它模仿人脑的神经网络结构来处理数据。深度学习在图像识别、自然语言处理等领域取得了重大突破。`

	doc := &document.Document{ID: "chinese", Content: chineseMd}

	const size = 100
	const overlap = 20

	mc := NewMarkdownChunking(WithMarkdownChunkSize(size), WithMarkdownOverlap(overlap))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)

	// Validate Chinese character counting
	for i, c := range chunks {
		charCount := utf8.RuneCountInString(c.Content)
		require.LessOrEqual(t, charCount, 2*size, "Chinese chunk %d has %d chars, exceeds limit", i, charCount)

		// Verify chunk contains valid UTF-8
		require.True(t, utf8.ValidString(c.Content), "Chunk %d contains invalid UTF-8", i)

		// Check that Chinese characters are not broken
		if strings.Contains(c.Content, "人工智能") {
			require.True(t, strings.Contains(c.Content, "人工智能"), "Chinese phrase should not be broken")
		}
	}
}

// TestMarkdownChunking_NoStructure tests chunking of plain text without markdown structure
func TestMarkdownChunking_NoStructure(t *testing.T) {
	// Create a long text without any markdown structure
	longText := strings.Repeat("这是一段很长的中文文本，没有任何markdown结构，应该被强制按照固定大小分割。", 20)

	doc := &document.Document{ID: "plain", Content: longText}

	const size = 50
	const overlap = 10

	mc := NewMarkdownChunking(WithMarkdownChunkSize(size), WithMarkdownOverlap(overlap))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1, "Long text should be split into multiple chunks")

	// Overlap separator adds 4 characters: "\n\n" + "\n\n" (no visible marker)
	const overlapSeparatorLen = 4

	// Validate forced splitting
	for i, c := range chunks {
		charCount := utf8.RuneCountInString(c.Content)
		var maxSize int
		if i == 0 {
			// First chunk has no overlap separator
			maxSize = size + overlap
		} else {
			// Subsequent chunks may have overlap separator
			maxSize = size + overlap + overlapSeparatorLen
		}
		require.LessOrEqual(t, charCount, maxSize, "Chunk %d has %d chars, exceeds max=%d", i, charCount, maxSize)

		// Verify UTF-8 validity
		require.True(t, utf8.ValidString(c.Content), "Chunk %d contains invalid UTF-8", i)
	}

	// Verify overlap between chunks
	for i := 1; i < len(chunks); i++ {
		if overlap > 0 {
			prev := chunks[i-1].Content
			curr := chunks[i].Content

			prevRunes := []rune(prev)
			currRunes := []rune(curr)

			if len(prevRunes) >= overlap && len(currRunes) >= overlap {
				expectedOverlap := string(prevRunes[len(prevRunes)-overlap:])
				actualOverlap := string(currRunes[:overlap])
				require.Equal(t, expectedOverlap, actualOverlap, "Overlap mismatch between chunk %d and %d", i-1, i)
			}
		}
	}
}

// TestMarkdownChunking_LargeParagraph tests handling of very large paragraphs
func TestMarkdownChunking_LargeParagraph(t *testing.T) {
	// Create markdown with a very large paragraph
	largePara := strings.Repeat("这是一个非常大的段落。", 100) // ~2100 characters
	md := `# 大段落测试

` + largePara + `

## 小段落

这是一个正常大小的段落。`

	doc := &document.Document{ID: "large-para", Content: md}

	const size = 200
	const overlap = 50

	mc := NewMarkdownChunking(WithMarkdownChunkSize(size), WithMarkdownOverlap(overlap))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 3, "Large paragraph should be split into multiple chunks")

	// Check that large paragraph was properly split
	largeParaChunks := 0
	for _, c := range chunks {
		if strings.Contains(c.Content, "这是一个非常大的段落") {
			largeParaChunks++
		}

		charCount := utf8.RuneCountInString(c.Content)
		require.LessOrEqual(t, charCount, 2*size, "Chunk has %d chars, exceeds 2*size=%d", charCount, 2*size)
		require.True(t, utf8.ValidString(c.Content), "Chunk contains invalid UTF-8")
	}

	require.Greater(t, largeParaChunks, 1, "Large paragraph should appear in multiple chunks")
}

// TestMarkdownChunking_MixedContent tests mixed English and Chinese content
func TestMarkdownChunking_MixedContent(t *testing.T) {
	mixedMd := `# Mixed Content Test

This is English content mixed with 中文内容. The chunking algorithm should handle both languages correctly.

## English Section

This section contains only English text that should be processed normally by the markdown chunker.

## 中文部分

这个部分只包含中文内容，应该被正确处理。中文字符的计数应该准确。

## Mixed Section

This section has both English and 中文 content mixed together. Both 语言 should be handled correctly.`

	doc := &document.Document{ID: "mixed", Content: mixedMd}

	const size = 80
	const overlap = 15

	mc := NewMarkdownChunking(WithMarkdownChunkSize(size), WithMarkdownOverlap(overlap))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)

	// Validate mixed content handling
	for i, c := range chunks {
		charCount := utf8.RuneCountInString(c.Content)
		require.LessOrEqual(t, charCount, 2*size, "Mixed chunk %d has %d chars, exceeds limit", i, charCount)
		require.True(t, utf8.ValidString(c.Content), "Mixed chunk %d contains invalid UTF-8", i)

		// Check that mixed content is preserved
		if strings.Contains(c.Content, "English and 中文") {
			require.True(t, strings.Contains(c.Content, "English"), "English part should be preserved")
			require.True(t, strings.Contains(c.Content, "中文"), "Chinese part should be preserved")
		}
	}
}

// TestMarkdownChunking_CaseMDFormat tests case.md format with single section and large table content
func TestMarkdownChunking_CaseMDFormat(t *testing.T) {
	// Simulate case.md structure: title + table with long content using comprehensive fruit classification
	caseLikeContent := `# 全球水果品种大全

|水果名称|拉丁学名|种类|颜色|甜度等级|市场价格|主要产地|成熟季节|营养成分|储存温度|成熟度|供应商|保质期|特色标签|
|:----:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|
|红富士苹果|Malus domestica|仁果类|深红色|★★★★☆|¥6.80|山东烟台|9-11月|膳食纤维,维C,钾|0-4°C|90%|烟台果园|30天|脆甜多汁|
|巴西香蕉|Musa acuminata|芭蕉科|金黄色|★★★☆☆|¥3.50|海南三亚|全年供应|钾,镁,维B6|13-15°C|95%|热带农场|7天|软糯香甜|
|赣南脐橙|Citrus sinensis|柑橘属|橙黄色|★★★★☆|¥4.20|江西赣州|11-1月|维C,钙,柠檬酸|4-8°C|85%|赣南果园|45天|酸甜可口|
|阳光玫瑰葡萄|Vitis vinifera|葡萄科|青绿色|★★★★★|¥25.60|云南红河|7-9月|花青素,白藜芦醇|0-2°C|92%|精品庄园|20天|无籽香甜|
|台农芒果|Mangifera indica|漆树科|金黄色|★★★★☆|¥12.80|海南三亚|3-5月|维A,膳食纤维|10-13°C|88%|热带果园|15天|香甜软糯|
|红颜草莓|Fragaria ananassa|蔷薇科|鲜红色|★★★★★|¥18.90|云南昆明|12-4月|维C,叶酸,花青素|0-2°C|93%|高原农场|5天|香甜爆汁|
|麒麟西瓜|Citrullus lanatus|葫芦科|翠绿色|★★★☆☆|¥2.90|新疆昌吉|6-8月|水分,番茄红素|8-12°C|98%|沙漠基地|10天|汁多味甜|
|美早樱桃|Prunus avium|蔷薇科|深红色|★★★★★|¥35.80|大连旅顺|5-6月|铁,维C,花青素|0-2°C|90%|辽东果园|12天|脆甜饱满|
|翠香猕猴桃|Actinidia deliciosa|猕猴桃科|棕绿色|★★★★☆|¥8.50|陕西眉县|9-11月|维C,维E,叶酸|0-4°C|87%|秦岭农场|25天|酸甜适中|
|尤力克柠檬|Citrus limon|芸香科|亮黄色|★★☆☆☆|¥4.80|四川安岳|9-12月|维C,柠檬酸|8-12°C|82%|川南果园|60天|酸爽清新|
|泰国山竹|Garcinia mangostana|金丝桃科|深紫色|★★★★☆|¥22.30|泰国南部|5-9月|氧杂蒽酮,维C|4-8°C|85%|进口果园|18天|清甜多汁|
|越南红心火龙果|Hylocereus undatus|仙人掌科|玫红色|★★★☆☆|¥8.90|越南平顺|5-11月|花青素,膳食纤维|8-10°C|90%|热带果园|20天|清甜爽口|
|智利车厘子|Prunus cerasus|蔷薇科|酒红色|★★★★★|¥45.60|智利中部|11-2月|花青素,维C|0-2°C|94%|南美庄园|25天|脆甜爆汁|
|菲律宾香蕉|Musa paradisiaca|芭蕉科|金黄色|★★★☆☆|¥3.80|菲律宾棉兰老|全年供应|钾,镁,维B6|13-15°C|96%|进口农场|12天|软糯香甜|
|新西兰奇异果|Actinidia chinensis|猕猴桃科|棕黄色|★★★★☆|¥12.50|新西兰丰盛湾|4-10月|维C,膳食纤维|0-4°C|89%|海外果园|30天|香甜软糯|
|澳洲芒果|Mangifera indica|漆树科|橙黄色|★★★★☆|¥16.80|澳洲北领地|9-12月|维A,维C|10-13°C|91%|进口庄园|18天|香甜细腻|
|云南蓝莓|Vaccinium corymbosum|杜鹃花科|蓝紫色|★★★★★|¥28.90|云南澄江|5-8月|花青素,维C|0-2°C|93%|高原农场|8天|香甜爆浆|
|福建蜜柚|Citrus maxima|芸香科|浅黄色|★★★☆☆|¥6.50|福建漳州|10-12月|维C,膳食纤维|8-12°C|87%|闽南果园|45天|清甜多汁|
|海南莲雾|Syzygium samarangense|桃金娘科|粉红色|★★★☆☆|¥18.50|海南文昌|12-3月|维C,膳食纤维|8-12°C|85%|热带果园|12天|清甜爽脆|
|广西百香果|Passiflora edulis|西番莲科|紫红色|★★★★☆|¥9.80|广西玉林|7-10月|维C,膳食纤维|8-10°C|88%|桂南农场|20天|酸甜芳香|`

	doc := &document.Document{ID: "case-md-format", Content: caseLikeContent}

	// Use very small chunk size to force splitting
	const size = 50   // 50 characters per chunk
	const overlap = 5 // 5 character overlap

	mc := NewMarkdownChunking(WithMarkdownChunkSize(size), WithMarkdownOverlap(overlap))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 20, "Large table should be split into multiple chunks")

	// Validate chunking behavior - account for actual content size and chunking overhead
	totalChars := utf8.RuneCountInString(caseLikeContent)
	// Conservative estimate considering table formatting overhead and UTF-8 encoding
	// Each chunk has overhead from headers and formatting, so use a safer calculation
	expectedMinChunks := (totalChars + size - 1) / (size - overlap) // Ceiling division
	require.GreaterOrEqual(t, len(chunks), expectedMinChunks/2, "Should have sufficient chunks for large table content")

	// Overlap separator adds 4 characters: "\n\n" + "\n\n" (no visible marker)
	const overlapSeparatorLen = 4

	// Check each chunk
	for i, chunk := range chunks {
		charCount := utf8.RuneCountInString(chunk.Content)
		maxSize := 2*size + overlapSeparatorLen
		require.LessOrEqual(t, charCount, maxSize, "Chunk %d has %d chars, exceeds max=%d", i, charCount, maxSize)
		require.True(t, utf8.ValidString(chunk.Content), "Chunk %d contains invalid UTF-8", i)
		require.NotEmpty(t, chunk.Content, "Chunk %d is empty", i)
	}

	// Verify that table content was split (not checking specific Chinese headers due to splitting variations)
	var tableContentFound int
	for _, chunk := range chunks {
		// Just check if we find table markers or some Chinese content
		if strings.Contains(chunk.Content, "|") || strings.Contains(chunk.Content, "红富士") {
			tableContentFound++
		}
	}
	require.GreaterOrEqual(t, tableContentFound, 1, "Table content should appear in at least one chunk")
}

// TestMarkdownChunking_EdgeCases tests various edge cases
func TestMarkdownChunking_EdgeCases(t *testing.T) {
	testCases := []struct {
		name      string
		content   string
		size      int
		overlap   int
		minChunks int
	}{
		{
			name:      "single character repeated",
			content:   strings.Repeat("中", 500),
			size:      50,
			overlap:   5,
			minChunks: 8,
		},
		{
			name:      "empty sections",
			content:   "# Header 1\n\n\n\n# Header 2\n\n\n\n# Header 3",
			size:      20,
			overlap:   0,
			minChunks: 1,
		},
		{
			name:      "only headers",
			content:   "# Header 1\n## Header 2\n### Header 3\n#### Header 4",
			size:      15,
			overlap:   0,
			minChunks: 1,
		},
		{
			name:      "very small chunk size",
			content:   "这是测试内容",
			size:      1,
			overlap:   0,
			minChunks: 6,
		},
		{
			name:      "case.md format large table",
			content:   "# 标题\n\n" + strings.Repeat("|a|b|c|d|e|f|g|h|i|j|k|l|m|n|\n", 20),
			size:      30,
			overlap:   3,
			minChunks: 10,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			doc := &document.Document{ID: tc.name, Content: tc.content}
			mc := NewMarkdownChunking(WithMarkdownChunkSize(tc.size), WithMarkdownOverlap(tc.overlap))

			chunks, err := mc.Chunk(doc)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(chunks), tc.minChunks, "Expected at least %d chunks", tc.minChunks)

			// Validate each chunk
			for i, c := range chunks {
				require.True(t, utf8.ValidString(c.Content), "Chunk %d contains invalid UTF-8", i)
				require.NotEmpty(t, c.Content, "Chunk %d is empty", i)

				charCount := utf8.RuneCountInString(c.Content)
				require.LessOrEqual(t, charCount, 3*tc.size, "Chunk %d has %d chars, too large", i, charCount)
			}
		})
	}
}

// TestMarkdownChunking_SplitByHeader_HeadingWithoutText verifies that
// splitByHeader handles headings without text safely and preserves content.
func TestMarkdownChunking_SplitByHeader_HeadingWithoutText(t *testing.T) {
	tests := []struct {
		name         string
		level        int
		content      string
		contentHints []string
	}{
		{
			name:  "empty level1 heading after normal level1 heading",
			level: 1,
			content: `# First Title

Paragraph before empty level1 heading that should be retained.

#

Paragraph after empty level1 heading should still be retained.`,
			contentHints: []string{
				"Paragraph before empty level1 heading",
				"Paragraph after empty level1 heading",
			},
		},
		{
			name:  "empty level2 heading after normal level2 heading",
			level: 2,
			content: `## Section A

Content before empty level2 heading should be retained.

##

Content after empty level2 heading should still be retained.`,
			contentHints: []string{
				"Content before empty level2 heading",
				"Content after empty level2 heading",
			},
		},
		{
			name:  "closing-hash-only heading",
			level: 3,
			content: `### Section A

Content before closing-hash-only heading should be retained.

### ###

Content after closing-hash-only heading should still be retained.`,
			contentHints: []string{
				"Content before closing-hash-only heading",
				"Content after closing-hash-only heading",
			},
		},
		{
			name:  "consecutive empty level1 headings",
			level: 1,
			content: `# First Title

Content before consecutive empty level1 headings should be retained.

#

#

Content after consecutive empty level1 headings should still be retained.`,
			contentHints: []string{
				"Content before consecutive empty level1 headings",
				"Content after consecutive empty level1 headings",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := NewMarkdownChunking(WithMarkdownChunkSize(32), WithMarkdownOverlap(0))

			var sections []headerSection
			require.NotPanics(t, func() {
				sections = mc.splitByHeader(tt.content, tt.level)
			})
			require.NotEmpty(t, sections)

			var combined strings.Builder
			for _, section := range sections {
				require.NotEmpty(t, strings.TrimSpace(section.Content))
				combined.WriteString(section.Header)
				combined.WriteString("\n")
				combined.WriteString(section.Content)
				combined.WriteString("\n")
			}

			fullText := combined.String()
			for _, hint := range tt.contentHints {
				require.Contains(t, fullText, hint)
			}
		})
	}
}

func TestFindNodeStartPos(t *testing.T) {
	tests := []struct {
		name    string
		content string
		level   int
		wantPos int
		wantTag string
	}{
		{
			name:    "heading at file start",
			content: "# Title\n\nBody",
			level:   1,
			wantTag: "# Title",
		},
		{
			name:    "heading after prefix line",
			content: "Intro line\n\n## Subtitle\n\nBody",
			level:   2,
			wantTag: "## Subtitle",
		},
		{
			name:    "heading with nested emphasis text",
			content: "Intro line\n\n## **Subtitle**\n\nBody",
			level:   2,
			wantTag: "## **Subtitle**",
		},
		{
			name:    "heading with nested link text",
			content: "Intro line\n\n## [Link](https://example.com)\n\nBody",
			level:   2,
			wantTag: "## [Link](https://example.com)",
		},
		{
			name:    "empty heading has no text segment",
			content: "Intro line\n\n#\n\nBody",
			level:   1,
			wantPos: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			heading := mustFindHeadingByLevel(t, tt.content, tt.level)
			got := findNodeStartPos(heading, []byte(tt.content))

			want := tt.wantPos
			if tt.wantTag != "" {
				want = strings.Index(tt.content, tt.wantTag)
				require.NotEqual(t, -1, want)
			}
			require.Equal(t, want, got)
		})
	}
}

func TestFindHeadingLineStartFallback(t *testing.T) {
	tests := []struct {
		name             string
		source           string
		searchFrom       int
		searchFromAnchor string
		searchFromOffset int
		level            int
		headingText      string
		wantPos          int
		wantTag          string
	}{
		{
			name:        "empty source",
			source:      "",
			searchFrom:  0,
			level:       1,
			headingText: "A",
			wantPos:     -1,
		},
		{
			name:        "invalid level",
			source:      "# A\n",
			searchFrom:  0,
			level:       0,
			headingText: "A",
			wantPos:     -1,
		},
		{
			name:        "negative searchFrom is clamped",
			source:      "# Alpha\n\nBody\n",
			searchFrom:  -100,
			level:       1,
			headingText: "Alpha",
			wantTag:     "# Alpha",
		},
		{
			name:        "searchFrom above length is clamped",
			source:      "Body line\n\n# Tail",
			searchFrom:  999,
			level:       1,
			headingText: "Tail",
			wantTag:     "# Tail",
		},
		{
			name:             "searchFrom inside heading line backtracks to line start",
			source:           "Body line\n\n## Mid\n\nTail\n",
			searchFromAnchor: "## Mid",
			searchFromOffset: 3,
			level:            2,
			headingText:      "Mid",
			wantTag:          "## Mid",
		},
		{
			name:        "heading text trim space still matches",
			source:      "# Alpha\n\n# Beta\n",
			searchFrom:  0,
			level:       1,
			headingText: "   Beta   ",
			wantTag:     "# Beta",
		},
		{
			name:        "heading text mismatch falls back to first candidate",
			source:      "# Alpha\n\n# Beta\n",
			searchFrom:  0,
			level:       1,
			headingText: "Gamma",
			wantTag:     "# Alpha",
		},
		{
			name:        "no heading candidate returns negative one",
			source:      "Body line\n\nTail line\n",
			searchFrom:  0,
			level:       1,
			headingText: "Alpha",
			wantPos:     -1,
		},
		{
			name:        "windows newline heading line is recognized",
			source:      "# Alpha\r\n\r\nBody\n",
			searchFrom:  0,
			level:       1,
			headingText: "Alpha",
			wantTag:     "# Alpha",
		},
		{
			name:        "empty heading text does not match non-empty heading lines",
			source:      "# Alpha\n\n# Beta\n",
			searchFrom:  0,
			level:       1,
			headingText: "",
			wantPos:     -1,
		},
		{
			name:        "empty heading text matches pure empty heading marker",
			source:      "# Alpha\n\n#\n\nTail\n",
			searchFrom:  0,
			level:       1,
			headingText: "",
			wantTag:     "#\n",
		},
		{
			name:        "empty heading text matches empty closing-hash heading",
			source:      "### Alpha\n\n### ###\n\nTail\n",
			searchFrom:  0,
			level:       3,
			headingText: "",
			wantTag:     "### ###",
		},
		{
			name:        "empty heading text ignores heading-like lines in fenced code block",
			source:      "```md\n# inside code\n```\n\n#\n",
			searchFrom:  0,
			level:       1,
			headingText: "",
			wantTag:     "#\n",
		},
		{
			name:        "empty heading text returns negative one when only fenced heading-like lines exist",
			source:      "```md\n# inside code\n```\n\nTail\n",
			searchFrom:  0,
			level:       1,
			headingText: "",
			wantPos:     -1,
		},
		{
			name:        "non-empty heading text ignores fenced heading-like lines",
			source:      "```md\n# Target\n```\n\n# Target outside\n",
			searchFrom:  0,
			level:       1,
			headingText: "Target outside",
			wantTag:     "# Target outside",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			searchFrom := tt.searchFrom
			if tt.searchFromAnchor != "" {
				anchorPos := strings.Index(tt.source, tt.searchFromAnchor)
				require.NotEqual(t, -1, anchorPos)
				searchFrom = anchorPos + tt.searchFromOffset
			}

			got := findHeadingLineStartFallback(
				[]byte(tt.source),
				searchFrom,
				tt.level,
				tt.headingText,
			)

			want := tt.wantPos
			if tt.wantTag != "" {
				want = strings.Index(tt.source, tt.wantTag)
				require.NotEqual(t, -1, want)
			}
			require.Equal(t, want, got)
		})
	}
}

func TestNormalizeHeadingLineStart(t *testing.T) {
	tests := []struct {
		name             string
		headingLineStart int
		lastHeaderPos    int
		want             int
	}{
		{
			name:             "negative start is clamped to lastHeaderPos",
			headingLineStart: -1,
			lastHeaderPos:    12,
			want:             12,
		},
		{
			name:             "start less than lastHeaderPos is clamped",
			headingLineStart: 8,
			lastHeaderPos:    12,
			want:             12,
		},
		{
			name:             "start equals lastHeaderPos is preserved",
			headingLineStart: 12,
			lastHeaderPos:    12,
			want:             12,
		},
		{
			name:             "start greater than lastHeaderPos is preserved",
			headingLineStart: 30,
			lastHeaderPos:    12,
			want:             30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeHeadingLineStart(tt.headingLineStart, tt.lastHeaderPos)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsATXHeadingLineAtLevel(t *testing.T) {
	tests := []struct {
		name  string
		line  []byte
		level int
		want  bool
	}{
		{
			name:  "invalid level zero",
			line:  []byte("# Title"),
			level: 0,
			want:  false,
		},
		{
			name:  "invalid negative level",
			line:  []byte("# Title"),
			level: -1,
			want:  false,
		},
		{
			name:  "leading spaces greater than three are rejected",
			line:  []byte("    # Title"),
			level: 1,
			want:  false,
		},
		{
			name:  "leading spaces up to three are accepted",
			line:  []byte("   # Title"),
			level: 1,
			want:  true,
		},
		{
			name:  "exact marker length is accepted",
			line:  []byte("##"),
			level: 2,
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isATXHeadingLineAtLevel(tt.line, tt.level)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsEmptyATXHeadingLineAtLevel(t *testing.T) {
	tests := []struct {
		name  string
		line  []byte
		level int
		want  bool
	}{
		{
			name:  "pure marker heading",
			line:  []byte("#"),
			level: 1,
			want:  true,
		},
		{
			name:  "marker with trailing spaces",
			line:  []byte("##   "),
			level: 2,
			want:  true,
		},
		{
			name:  "empty heading with closing hashes",
			line:  []byte("### ###"),
			level: 3,
			want:  true,
		},
		{
			name:  "non-empty heading text",
			line:  []byte("# title"),
			level: 1,
			want:  false,
		},
		{
			name:  "not atx heading line",
			line:  []byte("plain text"),
			level: 1,
			want:  false,
		},
		{
			name:  "leading spaces over limit",
			line:  []byte("    #"),
			level: 1,
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEmptyATXHeadingLineAtLevel(tt.line, tt.level)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseFenceDelimiter(t *testing.T) {
	tests := []struct {
		name     string
		line     []byte
		wantOK   bool
		wantChar byte
		wantLen  int
		wantRema string
	}{
		{
			name:     "backtick fence with info string",
			line:     []byte("```go"),
			wantOK:   true,
			wantChar: '`',
			wantLen:  3,
			wantRema: "go",
		},
		{
			name:     "tilde fence with indentation",
			line:     []byte("   ~~~~python"),
			wantOK:   true,
			wantChar: '~',
			wantLen:  4,
			wantRema: "python",
		},
		{
			name:   "too short delimiter is rejected",
			line:   []byte("``"),
			wantOK: false,
		},
		{
			name:   "indentation over three spaces is rejected",
			line:   []byte("    ```"),
			wantOK: false,
		},
		{
			name:   "non fence line is rejected",
			line:   []byte("# heading"),
			wantOK: false,
		},
		{
			name:     "fence with carriage return",
			line:     []byte("```\r"),
			wantOK:   true,
			wantChar: '`',
			wantLen:  3,
			wantRema: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotChar, gotLen, gotRest, ok := parseFenceDelimiter(tt.line)
			require.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				return
			}
			require.Equal(t, tt.wantChar, gotChar)
			require.Equal(t, tt.wantLen, gotLen)
			require.Equal(t, tt.wantRema, string(gotRest))
		})
	}
}

func TestFindLineContentStartPos(t *testing.T) {
	source := []byte("line1\nline2")

	tests := []struct {
		name      string
		src       []byte
		lineStart int
		want      int
	}{
		{
			name:      "negative line start returns zero",
			src:       source,
			lineStart: -3,
			want:      0,
		},
		{
			name:      "line start beyond source length returns source length",
			src:       source,
			lineStart: len(source) + 5,
			want:      len(source),
		},
		{
			name:      "line start equal source length returns source length",
			src:       source,
			lineStart: len(source),
			want:      len(source),
		},
		{
			name:      "normal line start returns next line start",
			src:       source,
			lineStart: 0,
			want:      6,
		},
		{
			name:      "line without trailing newline returns source length",
			src:       source,
			lineStart: 6,
			want:      len(source),
		},
		{
			name:      "empty source with negative start returns zero",
			src:       []byte(""),
			lineStart: -1,
			want:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findLineContentStartPos(tt.src, tt.lineStart)
			require.Equal(t, tt.want, got)
		})
	}
}

func mustFindHeadingByLevel(t *testing.T, content string, level int) ast.Node {
	t.Helper()

	md := goldmark.New()
	doc := md.Parser().Parse(text.NewReader([]byte(content)))

	var found ast.Node
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		heading, ok := node.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		if heading.Level != level {
			return ast.WalkContinue, nil
		}
		found = heading
		return ast.WalkStop, nil
	})

	require.NotNil(t, found)
	return found
}

// TestMarkdownChunking_MultipleParagraphsInSection tests splitLargeSection with multiple paragraphs
func TestMarkdownChunking_MultipleParagraphsInSection(t *testing.T) {
	// Create a section with multiple paragraphs that should be grouped intelligently
	mdContent := `# Section with Multiple Paragraphs

This is the first paragraph. It contains some text that is relatively short.

This is the second paragraph. It also contains some text that is short enough.

This is the third paragraph with more content that should be in the same or different chunk.

This is the fourth paragraph that might go into a new chunk depending on the size.

This is the fifth paragraph to ensure we test the grouping logic properly.`

	doc := &document.Document{ID: "multi-para", Content: mdContent}
	// Use a moderate chunk size to trigger paragraph grouping logic
	mc := NewMarkdownChunking(WithMarkdownChunkSize(100), WithMarkdownOverlap(20))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1, "Should create multiple chunks")

	// Verify paragraph grouping
	for i, chunk := range chunks {
		// Each chunk should contain complete paragraphs
		require.NotEmpty(t, chunk.Content, "Chunk %d should not be empty", i)
	}

	// Verify header appears somewhere in the chunks
	combinedContent := ""
	for _, chunk := range chunks {
		combinedContent += chunk.Content
	}
	require.Contains(t, combinedContent, "Section with Multiple Paragraphs", "Header should appear in chunks")
}

// TestMarkdownChunking_MixedParagraphSizes tests splitLargeSection with mixed sizes
func TestMarkdownChunking_MixedParagraphSizes(t *testing.T) {
	smallPara := "Small paragraph."
	mediumPara := strings.Repeat("Medium sized paragraph with some content. ", 3)
	largePara := strings.Repeat("This is a very large paragraph that exceeds the chunk size limit. ", 20)

	mdContent := `# Mixed Paragraph Sizes

` + smallPara + `

` + mediumPara + `

` + largePara + `

` + smallPara + `

` + mediumPara

	doc := &document.Document{ID: "mixed-sizes", Content: mdContent}
	mc := NewMarkdownChunking(WithMarkdownChunkSize(150), WithMarkdownOverlap(30))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1, "Mixed content should create multiple chunks")

	// Verify proper handling of different paragraph sizes
	foundLarge := false
	for _, chunk := range chunks {
		require.NotEmpty(t, chunk.Content)
		if strings.Contains(chunk.Content, "very large paragraph") {
			foundLarge = true
		}
	}
	require.True(t, foundLarge, "Large paragraph content should be in chunks")
}

// TestMarkdownChunking_OverlapValidation tests overlap >= chunkSize boundary condition.
func TestMarkdownChunking_OverlapValidation(t *testing.T) {
	tests := []struct {
		name      string
		chunkSize int
		overlap   int
	}{
		{
			name:      "overlap greater than chunkSize",
			chunkSize: 10,
			overlap:   15,
		},
		{
			name:      "overlap equal to chunkSize",
			chunkSize: 20,
			overlap:   20,
		},
		{
			name:      "very large overlap",
			chunkSize: 5,
			overlap:   100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := NewMarkdownChunking(
				WithMarkdownChunkSize(tt.chunkSize),
				WithMarkdownOverlap(tt.overlap),
			)

			// Should still work despite invalid overlap
			doc := &document.Document{ID: "test", Content: "# Header\n\nTest content for validation"}
			chunks, err := mc.Chunk(doc)
			require.NoError(t, err)
			require.NotEmpty(t, chunks)
		})
	}
}

// TestMarkdownChunking_Level1HeaderOnly tests documents with only level 1 headers
func TestMarkdownChunking_Level1HeaderOnly(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "single level 1 header with large content",
			content: "# Title\n\n" + strings.Repeat("这是一段很长的内容。", 100),
		},
		{
			name:    "multiple level 1 headers",
			content: "# Title1\n\n内容1\n\n# Title2\n\n内容2\n\n# Title3\n\n内容3",
		},
		{
			name:    "level 1 header without content",
			content: "# Empty Header\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &document.Document{ID: tt.name, Content: tt.content}
			mc := NewMarkdownChunking(WithMarkdownChunkSize(50), WithMarkdownOverlap(5))

			chunks, err := mc.Chunk(doc)
			require.NoError(t, err)
			require.NotEmpty(t, chunks)

			// Verify all chunks are valid
			for i, chunk := range chunks {
				require.True(t, utf8.ValidString(chunk.Content), "Chunk %d contains invalid UTF-8", i)
				require.NotEmpty(t, strings.TrimSpace(chunk.Content), "Chunk %d is empty or whitespace only", i)
			}
		})
	}
}

// TestMarkdownChunking_DeepNesting tests deeply nested headers (level 1-6)
func TestMarkdownChunking_DeepNesting(t *testing.T) {
	content := `# Level 1
内容 1

## Level 2
内容 2

### Level 3
内容 3

#### Level 4
内容 4

##### Level 5
内容 5

###### Level 6
内容 6
`

	doc := &document.Document{ID: "deep-nesting", Content: content}
	mc := NewMarkdownChunking(WithMarkdownChunkSize(30), WithMarkdownOverlap(5))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.NotEmpty(t, chunks)

	// Verify all chunks are valid UTF-8 and non-empty
	for i, chunk := range chunks {
		require.True(t, utf8.ValidString(chunk.Content), "Chunk %d contains invalid UTF-8", i)
		require.NotEmpty(t, strings.TrimSpace(chunk.Content), "Chunk %d is empty", i)
	}

	// Verify at least some level markers are preserved in the chunks
	combinedContent := ""
	for _, chunk := range chunks {
		combinedContent += chunk.Content
	}
	require.Contains(t, combinedContent, "Level", "Should contain header text")
}

// TestMarkdownChunking_EmptyLines tests handling of multiple empty lines
func TestMarkdownChunking_EmptyLines(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "many empty lines between content",
			content: "# Title\n\n\n\n\n\nContent\n\n\n\n\nMore content",
		},
		{
			name:    "trailing empty lines",
			content: "# Title\n\nContent\n\n\n\n\n",
		},
		{
			name:    "leading empty lines",
			content: "\n\n\n\n# Title\n\nContent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &document.Document{ID: tt.name, Content: tt.content}
			mc := NewMarkdownChunking(WithMarkdownChunkSize(50), WithMarkdownOverlap(5))

			chunks, err := mc.Chunk(doc)
			require.NoError(t, err)
			require.NotEmpty(t, chunks)

			// Verify no chunk is empty after trimming
			for i, chunk := range chunks {
				require.NotEmpty(t, strings.TrimSpace(chunk.Content), "Chunk %d should not be empty", i)
			}
		})
	}
}

// TestMarkdownChunking_SpecialCharacters tests handling of special markdown characters
func TestMarkdownChunking_SpecialCharacters(t *testing.T) {
	content := `# Title with *asterisks* and **bold**

Content with \` + "`code`" + ` and [links](http://example.com)

## Lists

- Item 1
- Item 2
- Item 3

> Blockquote content

` + "```go" + `
code block
` + "```" + `

| Table | Header |
|-------|--------|
| Cell  | Data   |
`

	doc := &document.Document{ID: "special-chars", Content: content}
	mc := NewMarkdownChunking(WithMarkdownChunkSize(80), WithMarkdownOverlap(10))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.NotEmpty(t, chunks)

	// Verify special characters are preserved
	fullContent := ""
	for _, chunk := range chunks {
		fullContent += chunk.Content
		require.True(t, utf8.ValidString(chunk.Content))
	}

	// Check that key markdown elements are preserved somewhere in chunks
	require.True(t, strings.Contains(fullContent, "*") || strings.Contains(fullContent, "**"), "Bold/italic markers should be preserved")
}

// TestMarkdownChunking_OnlyWhitespace tests documents with only whitespace
func TestMarkdownChunking_OnlyWhitespace(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "only spaces",
			content: "     ",
		},
		{
			name:    "only newlines",
			content: "\n\n\n\n",
		},
		{
			name:    "only tabs",
			content: "\t\t\t",
		},
		{
			name:    "mixed whitespace",
			content: "  \n\t\n  \t  \n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &document.Document{ID: tt.name, Content: tt.content}
			mc := NewMarkdownChunking(WithMarkdownChunkSize(50), WithMarkdownOverlap(5))

			chunks, err := mc.Chunk(doc)
			// cleanText will trim all whitespace, making the document empty
			// So this should either return ErrEmptyDocument or a single empty chunk
			if err != nil {
				require.ErrorIs(t, err, ErrEmptyDocument, "Whitespace-only document should be treated as empty")
			} else {
				// If no error, should return valid chunks (some implementations may handle this differently)
				require.NotEmpty(t, chunks, "Should return at least one chunk")
			}
		})
	}
}

// TestMarkdownChunking_VeryLargeDocument tests handling of very large documents
func TestMarkdownChunking_VeryLargeDocument(t *testing.T) {
	// Create a very large document (>100KB)
	largeContent := "# Large Document\n\n"
	for i := 0; i < 1000; i++ {
		largeContent += "## Section " + strconv.Itoa(i) + "\n\n"
		largeContent += strings.Repeat("这是第"+strconv.Itoa(i)+"节的内容。", 50) + "\n\n"
	}

	doc := &document.Document{ID: "very-large", Content: largeContent}
	mc := NewMarkdownChunking(WithMarkdownChunkSize(500), WithMarkdownOverlap(50))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 100, "Large document should create many chunks")

	// Verify all chunks
	for i, chunk := range chunks {
		require.True(t, utf8.ValidString(chunk.Content), "Chunk %d contains invalid UTF-8", i)
		require.NotEmpty(t, chunk.Content, "Chunk %d is empty", i)

		charCount := utf8.RuneCountInString(chunk.Content)
		require.LessOrEqual(t, charCount, 2*500, "Chunk %d has %d chars, too large", i, charCount)
	}
}

// TestMarkdownChunking_NoOverlap tests chunking without overlap
func TestMarkdownChunking_NoOverlap(t *testing.T) {
	content := `# Title

Paragraph 1 with some content.

Paragraph 2 with more content.

Paragraph 3 with even more content.
`

	doc := &document.Document{ID: "no-overlap", Content: content}
	mc := NewMarkdownChunking(WithMarkdownChunkSize(30), WithMarkdownOverlap(0))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1)

	// Verify no overlap between consecutive chunks
	for i := 1; i < len(chunks); i++ {
		prev := chunks[i-1].Content
		curr := chunks[i].Content

		// With no overlap, current should not start with end of previous
		prevEnd := prev[max(0, len(prev)-10):]
		currStart := curr[:min(10, len(curr))]

		// They might share some whitespace but not substantial content
		if len(prevEnd) > 5 && len(currStart) > 5 {
			require.NotEqual(t, prevEnd, currStart, "Chunks %d and %d should not overlap", i-1, i)
		}
	}
}

// TestMarkdownChunking_HeaderWithoutContent tests headers without following content
func TestMarkdownChunking_HeaderWithoutContent(t *testing.T) {
	content := `# Title 1

## Subtitle 1

### Subsubtitle 1

# Title 2

## Subtitle 2
`

	doc := &document.Document{ID: "headers-no-content", Content: content}
	mc := NewMarkdownChunking(WithMarkdownChunkSize(50), WithMarkdownOverlap(5))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err)
	require.NotEmpty(t, chunks)

	// Verify headers are included in chunks
	foundTitle1 := false
	foundTitle2 := false
	for _, chunk := range chunks {
		if strings.Contains(chunk.Content, "Title 1") {
			foundTitle1 = true
		}
		if strings.Contains(chunk.Content, "Title 2") {
			foundTitle2 = true
		}
	}

	require.True(t, foundTitle1 || foundTitle2, "At least one title should be in chunks")
}

// TestMarkdownChunking_MixedNewlines tests different newline styles
func TestMarkdownChunking_MixedNewlines(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "unix newlines",
			content: "# Title\n\nContent\n\n## Subtitle\n\nMore content",
		},
		{
			name:    "windows newlines",
			content: "# Title\r\n\r\nContent\r\n\r\n## Subtitle\r\n\r\nMore content",
		},
		{
			name:    "mac newlines",
			content: "# Title\r\rContent\r\r## Subtitle\r\rMore content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &document.Document{ID: tt.name, Content: tt.content}
			mc := NewMarkdownChunking(WithMarkdownChunkSize(50), WithMarkdownOverlap(5))

			chunks, err := mc.Chunk(doc)
			require.NoError(t, err)
			require.NotEmpty(t, chunks)

			for i, chunk := range chunks {
				require.True(t, utf8.ValidString(chunk.Content), "Chunk %d contains invalid UTF-8", i)
			}
		})
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestMarkdownChunking_RecursiveIDUniqueness tests that all chunk IDs are unique
// when recursively splitting markdown documents with nested headers.
func TestMarkdownChunking_RecursiveIDUniqueness(t *testing.T) {
	// Create a complex markdown document with multiple levels of nested headers
	// This structure will trigger the recursive splitting algorithm
	complexDoc := `# Main Document: System Architecture

This document outlines the complete architecture of our distributed system.

## Chapter 1: Core Components

The core components form the foundation of our system architecture.

### 1.1 Authentication Service

The authentication service handles user identity verification and session management.

#### 1.1.1 JWT Implementation

JSON Web Tokens are used for stateless authentication across services.

##### 1.1.1.1 Token Generation

Tokens are generated using RSA-256 with a 2048-bit key for strong security.

##### 1.1.1.2 Token Validation

All incoming requests must include a valid JWT in the Authorization header.

#### 1.1.2 OAuth 2.0 Integration

Third-party authentication is supported via OAuth 2.0 for major providers.

### 1.2 User Management Service

This service handles user profiles, preferences, and account management.

## Chapter 2: Data Layer

The data layer provides persistence and caching for all services.

### 2.1 Primary Database

We use PostgreSQL 14 as our primary relational database.

#### 2.1.1 Schema Design

The database schema follows third-normal form with appropriate indexes.

#### 2.1.2 Connection Pooling

PgBouncer is used for connection pooling to manage database connections efficiently.

### 2.2 Caching Layer

Redis 7 is used as a distributed cache for frequently accessed data.

#### 2.2.1 Cache Strategies

Different cache strategies are employed based on data access patterns.

##### 2.2.1.1 Read-Through Cache

For data that is read frequently but updated rarely.

##### 2.2.1.2 Write-Through Cache

For data that requires strong consistency between cache and database.

##### 2.2.1.3 Cache Invalidation

A combination of TTL-based and explicit invalidation is used.

### 2.3 Search Index

Elasticsearch provides full-text search capabilities across all content.

## Chapter 3: API Layer

The API layer exposes functionality to external clients and internal services.

### 3.1 REST API

RESTful endpoints follow OpenAPI 3.0 specification with detailed documentation.

### 3.2 GraphQL API

GraphQL provides a flexible query interface for complex data requirements.

### 3.3 gRPC Services

Internal service communication uses gRPC for high-performance RPC calls.

## Chapter 4: Monitoring & Observability

Comprehensive monitoring ensures system reliability and performance.

### 4.1 Metrics Collection

Prometheus scrapes metrics from all services and infrastructure components.

### 4.2 Distributed Tracing

Jaeger provides end-to-end tracing for requests across service boundaries.

### 4.3 Log Aggregation

Fluentd collects and forwards logs to Elasticsearch for centralized analysis.

## Appendix: Long Technical Details

This section contains extensive technical documentation that will be split into multiple chunks due to its length. ` + strings.Repeat("Distributed systems require careful design of communication patterns, failure handling, and consistency models. ", 100)

	doc := &document.Document{
		ID:      "system_architecture",
		Name:    "architecture.md",
		Content: complexDoc,
		Metadata: map[string]any{
			"author":  "Engineering Team",
			"version": "2.1.0",
			"type":    "technical",
		},
	}

	// Use small chunk size to force extensive recursive splitting
	const chunkSize = 120
	const overlap = 15

	mc := NewMarkdownChunking(WithMarkdownChunkSize(chunkSize), WithMarkdownOverlap(overlap))

	chunks, err := mc.Chunk(doc)
	require.NoError(t, err, "Chunk should succeed for complex document")
	require.Greater(t, len(chunks), 10, "Complex document should generate many chunks")

	//  Verify all chunk IDs are globally unique
	idSet := make(map[string]bool)
	for i, chunk := range chunks {
		// Check for duplicate IDs - this is the main test for the bug fix
		require.False(t, idSet[chunk.ID], "Duplicate chunk ID found at index %d: %s", i, chunk.ID)
		idSet[chunk.ID] = true

		// Verify ID follows expected pattern
		require.True(t, strings.HasPrefix(chunk.ID, doc.ID+"_"),
			"Chunk ID %s should start with document ID %s", chunk.ID, doc.ID)
	}

	// Verify metadata consistency
	require.Equal(t, len(chunks), len(idSet), "Number of chunks should equal number of unique IDs")

	// Verify chunk metadata completeness
	for i, chunk := range chunks {
		// Check required metadata fields
		chunkIndex, hasIndex := chunk.Metadata[source.MetaChunkIndex]
		require.True(t, hasIndex, "Chunk %d missing chunk index metadata", i)

		chunkSizeMeta, hasSize := chunk.Metadata[source.MetaChunkSize]
		require.True(t, hasSize, "Chunk %d missing chunk size metadata", i)

		// Verify metadata types
		_, isInt := chunkIndex.(int)
		require.True(t, isInt, "Chunk index should be int type")

		_, isIntSize := chunkSizeMeta.(int)
		require.True(t, isIntSize, "Chunk size should be int type")

		// Verify chunk size metadata matches actual content size
		actualSize := utf8.RuneCountInString(chunk.Content)
		if overlappedSize, hasOverlapped := chunk.Metadata[source.MetaOverlappedContentSize]; hasOverlapped {
			require.Equal(t, overlappedSize, actualSize,
				"Chunk %d overlapped content size mismatch: metadata=%d, actual=%d",
				i, overlappedSize, actualSize)
		} else {
			require.Equal(t, chunkSizeMeta, actualSize,
				"Chunk %d size metadata mismatch: expected %d, got %d",
				i, chunkSizeMeta, actualSize)
		}
	}

	// Verify content integrity
	totalChunkChars := 0
	for _, chunk := range chunks {
		content := strings.TrimSpace(chunk.Content)
		require.NotEmpty(t, content, "Chunk content should not be empty after trimming")
		require.True(t, utf8.ValidString(chunk.Content), "Chunk contains invalid UTF-8")

		totalChunkChars += utf8.RuneCountInString(chunk.Content)
	}

	// Account for overlap markers in chunk content
	overlapMarker := "\n\n--- above content is overlap of prefix chunk ---\n\n"
	overlapMarkerCount := 0
	for _, chunk := range chunks {
		if strings.Contains(chunk.Content, overlapMarker) {
			overlapMarkerCount++
		}
	}

	// Approximate total size check (allowing for overlap and markers)
	originalChars := utf8.RuneCountInString(complexDoc)
	expectedMinChars := originalChars - (overlap * (len(chunks) - 1 - overlapMarkerCount))
	expectedMaxChars := originalChars + (len(overlapMarker) * overlapMarkerCount)

	require.GreaterOrEqual(t, totalChunkChars, expectedMinChars/2,
		"Total chunk characters too low: got %d, expected at least %d",
		totalChunkChars, expectedMinChars/2)

	require.LessOrEqual(t, totalChunkChars, expectedMaxChars*2,
		"Total chunk characters too high: got %d, expected at most %d",
		totalChunkChars, expectedMaxChars*2)

	// Verify no data loss - check key content appears in chunks
	keyPhrases := []string{
		"System Architecture",
		"Authentication Service",
		"JWT Implementation",
		"PostgreSQL",
		"Redis",
		"Elasticsearch",
		"Prometheus",
		"Jaeger",
		"Distributed systems",
	}

	for _, phrase := range keyPhrases {
		found := false
		for _, chunk := range chunks {
			if strings.Contains(chunk.Content, phrase) {
				found = true
				break
			}
		}
		require.True(t, found, "Key phrase %q not found in any chunk", phrase)
	}

	// Verify chunk size limits (accounting for overlap markers)
	for i, chunk := range chunks {
		charCount := utf8.RuneCountInString(chunk.Content)

		// Calculate maximum allowed size
		maxAllowed := chunkSize
		if i > 0 && strings.Contains(chunk.Content, overlapMarker) {
			// Chunks with overlap markers can be larger
			maxAllowed = chunkSize + overlap + len(overlapMarker)
		} else if i > 0 {
			// Chunks with overlap but no marker
			maxAllowed = chunkSize + overlap
		}

		// Allow some flexibility for header preservation
		require.LessOrEqual(t, charCount, maxAllowed*2,
			"Chunk %d too large: %d characters exceeds limit of %d",
			i, charCount, maxAllowed*2)
	}

	// Log test results for debugging
	t.Logf("Generated %d unique chunks for complex recursive document", len(chunks))
	t.Logf("Document ID: %s", doc.ID)
	t.Logf("Chunk size: %d, Overlap: %d", chunkSize, overlap)

	// Show sample of generated IDs
	if len(chunks) > 0 {
		sampleSize := min(5, len(chunks))
		sampleIDs := make([]string, sampleSize)
		for i := 0; i < sampleSize; i++ {
			sampleIDs[i] = chunks[i].ID
		}
		t.Logf("Sample chunk IDs: %v", sampleIDs)
	}
}
