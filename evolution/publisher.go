//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Publisher persists extracted skills.
type Publisher interface {
	UpsertSkill(ctx context.Context, spec *SkillSpec) error
	// DeleteSkill removes the skill directory matching the given name. It
	// MUST be a no-op when no such skill exists, so callers can use it
	// idempotently after a Refresh race.
	DeleteSkill(ctx context.Context, name string) error
}

// filePublisher writes each skill to a SKILL.md file under a managed
// directory on the local filesystem.
type filePublisher struct {
	root string
}

// newFilePublisher creates a filePublisher rooted at root.
func newFilePublisher(root string) *filePublisher {
	return &filePublisher{root: root}
}

// UpsertSkill implements Publisher. It creates (or overwrites) a SKILL.md file
// under root/<sanitized-name>/SKILL.md.
func (p *filePublisher) UpsertSkill(_ context.Context, spec *SkillSpec) error {
	if spec == nil {
		return errors.New("evolution: upsert skill: nil spec")
	}
	dir := filepath.Join(p.root, sanitizeSkillName(spec.Name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := renderSkillMarkdown(spec)
	target := filepath.Join(dir, "SKILL.md")
	return writeFileAtomically(target, []byte(content), 0o644)
}

// DeleteSkill implements Publisher. It removes root/<sanitized-name>/.
// Refuses to remove the root directory itself. Returns nil when the target
// does not exist so it is safe to call idempotently.
func (p *filePublisher) DeleteSkill(_ context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("evolution: delete skill: empty name")
	}
	safe := sanitizeSkillName(name)
	dir := filepath.Join(p.root, safe)
	rootClean := filepath.Clean(p.root)
	dirClean := filepath.Clean(dir)
	if dirClean == rootClean || dirClean == "." || dirClean == string(filepath.Separator) {
		return fmt.Errorf("evolution: delete skill: refuse to remove root %q", rootClean)
	}
	if _, err := os.Stat(dirClean); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := os.RemoveAll(dirClean); err != nil {
		return fmt.Errorf("evolution: delete skill: remove %q: %w", dirClean, err)
	}
	return nil
}
