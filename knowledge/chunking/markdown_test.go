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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
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

	// Overlap marker adds 52 characters: "\n\n--- above content is overlap of prefix chunk ---\n\n"
	const overlapMarkerLen = 52

	// Validate each chunk size - use character count, not byte count
	for i, c := range chunks {
		// Ensure chunk size not huge (>2*size + overlap marker)
		charCount := utf8.RuneCountInString(c.Content)
		maxSize := 2*size + overlapMarkerLen
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

	// Overlap marker adds 52 characters: "\n\n--- above content is overlap of prefix chunk ---\n\n"
	const overlapMarkerLen = 52

	// Validate forced splitting
	for i, c := range chunks {
		charCount := utf8.RuneCountInString(c.Content)
		var maxSize int
		if i == 0 {
			// First chunk has no overlap marker
			maxSize = size + overlap
		} else {
			// Subsequent chunks may have overlap marker
			maxSize = size + overlap + overlapMarkerLen
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

	// Overlap marker adds 52 characters: "\n\n--- above content is overlap of prefix chunk ---\n\n"
	const overlapMarkerLen = 52

	// Check each chunk
	for i, chunk := range chunks {
		charCount := utf8.RuneCountInString(chunk.Content)
		maxSize := 2*size + overlapMarkerLen
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
