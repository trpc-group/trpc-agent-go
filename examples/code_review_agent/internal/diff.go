// Package internal 提供代码评审 Agent 的核心实现。
package internal

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// LineType 表示 diff hunk 中一行的类型。
type LineType int

const (
	LineContext    LineType = iota // " " 上下文行
	LineAdd                        // "+" 新增行
	LineDelete                     // "-" 删除行
	LineHeader                     // diff 文件头 (diff --git, index, ---, +++)
	LineHunkHeader                 // "@@" hunk 头
	LineBinary                     // "Binary files differ"
)

// Line 表示 diff 中的一行。
type Line struct {
	Type    LineType
	Content string // 去掉 +/-/空格前缀后的原始文本
	OldNo   int    // 原始文件行号 (add-only 为 0)
	NewNo   int    // 新文件行号 (delete-only 为 0)
}

// Hunk 表示一个 @@ ... @@ 变更块。
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Header   string
	Lines    []Line
}

// DiffFile 表示 diff 中的一个文件。
type DiffFile struct {
	OldPath  string
	NewPath  string
	IsBinary bool
	Hunks    []Hunk
}

// AddedLines 返回该文件中所有新增行。
func (f DiffFile) AddedLines() []Line {
	var lines []Line
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Type == LineAdd {
				lines = append(lines, l)
			}
		}
	}
	return lines
}

// GoFile 判断是否为 Go 源文件。
func (f DiffFile) GoFile() bool {
	return strings.HasSuffix(f.NewPath, ".go") && !strings.HasSuffix(f.NewPath, "_test.go")
}

// TestFile 判断是否为测试文件。
func (f DiffFile) TestFile() bool {
	return strings.HasSuffix(f.NewPath, "_test.go")
}

var (
	// 匹配 diff --git a/path b/path
	reDiffGit = regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`)
	// 匹配 --- a/path 或 --- /dev/null
	reOldFile = regexp.MustCompile(`^--- (?:a/(.+)|/dev/null)`)
	// 匹配 +++ b/path 或 +++ /dev/null
	reNewFile = regexp.MustCompile(`^\+\+\+ (?:b/(.+)|/dev/null)`)
	// 匹配 @@ -oldStart,oldCount +newStart,newCount @@
	reHunkHeader = regexp.MustCompile(`^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@(.*)$`)
	// 匹配 Binary files differ
	reBinary = regexp.MustCompile(`^Binary files .+ differ$`)
)

// ParseDiff 解析 unified diff 文本，返回解析后的文件列表。
func ParseDiff(input string) ([]DiffFile, error) {
	if strings.TrimSpace(input) == "" {
		return nil, nil
	}

	lines := strings.Split(input, "\n")
	var files []DiffFile
	var currentFile *DiffFile
	var currentHunk *Hunk

	oldLineNo := 0
	newLineNo := 0

	for _, rawLine := range lines {
		// 跳过末尾空行
		if rawLine == "" && currentFile == nil {
			continue
		}

		// 检测文件头
		if strings.HasPrefix(rawLine, "diff --git ") {
			// 保存上一个文件
			if currentFile != nil {
				files = append(files, *currentFile)
			}
			currentFile = &DiffFile{}
			currentHunk = nil

			if m := reDiffGit.FindStringSubmatch(rawLine); m != nil {
				currentFile.OldPath = m[1]
				currentFile.NewPath = m[2]
			}
			continue
		}

		// 跳过 index, new file mode 等行
		if currentFile == nil {
			continue
		}
		if strings.HasPrefix(rawLine, "index ") ||
			strings.HasPrefix(rawLine, "new file mode ") ||
			strings.HasPrefix(rawLine, "deleted file mode ") ||
			strings.HasPrefix(rawLine, "old mode ") ||
			strings.HasPrefix(rawLine, "new mode ") ||
			strings.HasPrefix(rawLine, "similarity index ") ||
			strings.HasPrefix(rawLine, "rename from ") ||
			strings.HasPrefix(rawLine, "rename to ") ||
			strings.HasPrefix(rawLine, "copy from ") ||
			strings.HasPrefix(rawLine, "copy to ") {
			continue
		}

		// 检测 --- 行
		if strings.HasPrefix(rawLine, "--- ") {
			if m := reOldFile.FindStringSubmatch(rawLine); m != nil {
				if m[1] != "" {
					currentFile.OldPath = m[1]
				}
			}
			continue
		}

		// 检测 +++ 行
		if strings.HasPrefix(rawLine, "+++ ") {
			if m := reNewFile.FindStringSubmatch(rawLine); m != nil {
				if m[1] != "" {
					currentFile.NewPath = m[1]
				}
			}
			continue
		}

		// 检测 Binary 文件
		if reBinary.MatchString(rawLine) {
			currentFile.IsBinary = true
			continue
		}

		// 检测 hunk header
		if strings.HasPrefix(rawLine, "@@") {
			if m := reHunkHeader.FindStringSubmatch(rawLine); m != nil {
				oldStart, _ := strconv.Atoi(m[1])
				oldCount := 1
				if m[2] != "" {
					oldCount, _ = strconv.Atoi(m[2])
				}
				newStart, _ := strconv.Atoi(m[3])
				newCount := 1
				if m[4] != "" {
					newCount, _ = strconv.Atoi(m[4])
				}

				hunk := Hunk{
					OldStart: oldStart,
					OldCount: oldCount,
					NewStart: newStart,
					NewCount: newCount,
					Header:   strings.TrimSpace(m[5]),
				}
				currentFile.Hunks = append(currentFile.Hunks, hunk)
				currentHunk = &currentFile.Hunks[len(currentFile.Hunks)-1]

				oldLineNo = oldStart
				newLineNo = newStart
			}
			continue
		}

		// 处理 hunk 内容行
		if currentHunk != nil {
			var line Line
			switch {
			case strings.HasPrefix(rawLine, "+"):
				line = Line{Type: LineAdd, Content: rawLine[1:], NewNo: newLineNo}
				newLineNo++
			case strings.HasPrefix(rawLine, "-"):
				line = Line{Type: LineDelete, Content: rawLine[1:], OldNo: oldLineNo}
				oldLineNo++
			case strings.HasPrefix(rawLine, " "):
				line = Line{Type: LineContext, Content: rawLine[1:], OldNo: oldLineNo, NewNo: newLineNo}
				oldLineNo++
				newLineNo++
			case rawLine == "":
				// 空行在 diff 中可能出现，视为上下文
				line = Line{Type: LineContext, Content: "", OldNo: oldLineNo, NewNo: newLineNo}
				oldLineNo++
				newLineNo++
			default:
				// 跳过其他行（如 "\ No newline at end of file"）
				continue
			}
			currentHunk.Lines = append(currentHunk.Lines, line)
		}
	}

	// 保存最后一个文件
	if currentFile != nil {
		files = append(files, *currentFile)
	}

	return files, nil
}

// FormatLineRange 格式化行为 "file:line" 格式。
func FormatLineRange(file string, line int) string {
	if line <= 0 {
		return file
	}
	return fmt.Sprintf("%s:%d", file, line)
}
