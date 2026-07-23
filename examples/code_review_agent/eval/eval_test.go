//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package eval_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/orchestrator"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

type label struct {
	ExpectRules []string `json:"expect_rules"`
	HighRisk    bool     `json:"high_risk"`
	Clean       bool     `json:"clean"`
}

func TestHiddenSampleRates(t *testing.T) {
	root := moduleRoot(t)
	hiddenRoot := filepath.Join(root, "testdata", "hidden")
	entries, err := os.ReadDir(hiddenRoot)
	if err != nil {
		t.Fatal(err)
	}

	var tp, fn, fp, tn int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		labelPath := filepath.Join(hiddenRoot, name, "label.json")
		raw, err := os.ReadFile(labelPath)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var lb label
		if err := json.Unmarshal(raw, &lb); err != nil {
			t.Fatalf("%s label: %v", name, err)
		}

		out := t.TempDir()
		st, err := store.OpenSQLite(filepath.Join(out, "review.db"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := orchestrator.Run(context.Background(), orchestrator.Config{
			Mode:         review.ModeRuleOnly,
			Executor:     "local",
			Fixture:      name,
			FixturesRoot: hiddenRoot,
			SkillsRoot:   filepath.Join(root, "skills"),
			OutDir:       out,
			Store:        st,
			Runner:       sandbox.LocalRunner{},
		})
		_ = st.Close()
		if err != nil {
			t.Fatalf("%s run: %v", name, err)
		}

		got := map[string]bool{}
		for _, f := range res.Report.Findings {
			got[f.RuleID] = true
		}

		if lb.Clean {
			if len(res.Report.Findings) == 0 {
				tn++
			} else {
				fp++
				t.Logf("clean false positive on %s: %+v", name, res.Report.Findings)
			}
			continue
		}

		// High-risk samples: count detection of expected rules.
		hit := false
		for _, want := range lb.ExpectRules {
			if got[want] {
				hit = true
				break
			}
		}
		if hit {
			tp++
		} else {
			fn++
			t.Logf("missed expected rules on %s want=%v got=%v", name, lb.ExpectRules, got)
		}
	}

	detection := float64(tp) / float64(tp+fn)
	var fpr float64
	if fp+tn > 0 {
		fpr = float64(fp) / float64(fp+tn)
	}
	t.Logf("detection=%.2f (%d/%d) false_positive=%.2f (%d/%d)",
		detection, tp, tp+fn, fpr, fp, fp+tn)
	if detection < 0.80 {
		t.Fatalf("detection rate %.2f < 0.80", detection)
	}
	if fpr > 0.15 {
		t.Fatalf("false positive rate %.2f > 0.15", fpr)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(wd) == "eval" {
		return filepath.Dir(wd)
	}
	return wd
}
