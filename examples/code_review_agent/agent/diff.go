package agent

import (
	"bufio"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@\s?(.*)$`)
var packageRE = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_]*)\b`)

func ParseUnifiedDiff(raw string) (ReviewInput, error) {
	input := ReviewInput{RawDiff: raw}
	if strings.TrimSpace(raw) == "" {
		input.Summary.DiffSHA256 = SHA256Hex(raw)
		return input, nil
	}

	var current *ChangedFile
	var currentHunk *Hunk
	oldLine := 0
	newLine := 0

	scanner := bufio.NewScanner(strings.NewReader(raw))
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			if current != nil {
				input.Files = append(input.Files, *current)
			}
			current = &ChangedFile{}
			currentHunk = nil
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				current.OldPath = trimDiffPath(parts[2])
				current.NewPath = trimDiffPath(parts[3])
			}
		case strings.HasPrefix(line, "--- "):
			if current == nil {
				current = &ChangedFile{}
			}
			current.OldPath = trimDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "--- ")))
		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				current = &ChangedFile{}
			}
			current.NewPath = trimDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
		case strings.HasPrefix(line, "@@ "):
			if current == nil {
				return input, fmt.Errorf("hunk appears before file header")
			}
			m := hunkHeaderRE.FindStringSubmatch(line)
			if m == nil {
				return input, fmt.Errorf("invalid hunk header %q", line)
			}
			oldStart, _ := strconv.Atoi(m[1])
			oldCount := parseCount(m[2])
			newStart, _ := strconv.Atoi(m[3])
			newCount := parseCount(m[4])
			current.Hunks = append(current.Hunks, Hunk{
				OldStart: oldStart,
				OldLines: oldCount,
				NewStart: newStart,
				NewLines: newCount,
				Header:   m[5],
			})
			currentHunk = &current.Hunks[len(current.Hunks)-1]
			oldLine = oldStart
			newLine = newStart
		case currentHunk != nil:
			if line == `\ No newline at end of file` {
				continue
			}
			if line == "" {
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{Kind: "context", OldLine: oldLine, NewLine: newLine})
				oldLine++
				newLine++
				continue
			}
			prefix := line[0]
			content := ""
			if len(line) > 1 {
				content = line[1:]
			}
			switch prefix {
			case '+':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{Kind: "add", NewLine: newLine, Content: content})
				input.Summary.AddedLineCount++
				if current.Package == "" {
					current.Package = extractPackage(content)
				}
				newLine++
			case '-':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{Kind: "delete", OldLine: oldLine, Content: content})
				input.Summary.DeletedLineCount++
				oldLine++
			default:
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{Kind: "context", OldLine: oldLine, NewLine: newLine, Content: content})
				if current.Package == "" {
					current.Package = extractPackage(content)
				}
				oldLine++
				newLine++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return input, err
	}
	if current != nil {
		input.Files = append(input.Files, *current)
	}
	input.Summary.DiffSHA256 = SHA256Hex(raw)
	input.Summary.FileCount = len(input.Files)
	packages := map[string]bool{}
	for i := range input.Files {
		if input.Files[i].NewPath == "/dev/null" || input.Files[i].NewPath == "" {
			input.Files[i].NewPath = input.Files[i].OldPath
		}
		if strings.HasSuffix(input.Files[i].NewPath, ".go") {
			input.Summary.GoFileCount++
		}
		if input.Files[i].Package != "" {
			packages[input.Files[i].Package] = true
		}
	}
	for pkg := range packages {
		input.Summary.Packages = append(input.Summary.Packages, pkg)
	}
	sort.Strings(input.Summary.Packages)
	return input, nil
}

func ReviewInputFromFiles(files []string) ReviewInput {
	input := ReviewInput{}
	for _, f := range files {
		input.Files = append(input.Files, ChangedFile{NewPath: f})
		if strings.HasSuffix(f, ".go") {
			input.Summary.GoFileCount++
		}
	}
	input.Summary.FileCount = len(input.Files)
	input.Summary.DiffSHA256 = SHA256Hex(strings.Join(files, "\n"))
	return input
}

func trimDiffPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return path
}

func parseCount(s string) int {
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 1
	}
	return v
}

func extractPackage(line string) string {
	m := packageRE.FindStringSubmatch(line)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}
