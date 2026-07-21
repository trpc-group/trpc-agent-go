package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewerSandboxFailureDoesNotCrash(t *testing.T) {
	root := filepath.Dir(filepath.Dir(currentFileForTest()))
	out := filepath.Join(t.TempDir(), "out")
	cfg := Config{
		DiffFile:            filepath.Join(root, "testdata", "fixtures", "sandbox_failure.diff"),
		SkillPath:           filepath.Join(root, "skills", "code-review"),
		Runtime:             "fake",
		OutDir:              out,
		StorePath:           filepath.Join(out, "store.json"),
		DryRun:              true,
		RuleOnly:            true,
		Timeout:             time.Second,
		MaxOutputBytes:      1024,
		ForceSandboxFailure: true,
	}
	report, err := NewReviewer(cfg).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.SandboxRuns) == 0 || report.SandboxRuns[0].Status != "failed" {
		t.Fatalf("expected failed sandbox run: %+v", report.SandboxRuns)
	}
	if len(report.NeedsHumanReview) == 0 {
		t.Fatalf("expected sandbox failure to produce human review item")
	}
}

func TestReviewerReportRedactsSecrets(t *testing.T) {
	root := filepath.Dir(filepath.Dir(currentFileForTest()))
	out := filepath.Join(t.TempDir(), "out")
	cfg := Config{
		DiffFile:       filepath.Join(root, "testdata", "fixtures", "redaction.diff"),
		SkillPath:      filepath.Join(root, "skills", "code-review"),
		Runtime:        "fake",
		OutDir:         out,
		StorePath:      filepath.Join(out, "store.json"),
		DryRun:         true,
		RuleOnly:       true,
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	}
	report, err := NewReviewer(cfg).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(report.ReportJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	storeBody, err := os.ReadFile(cfg.StorePath)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(body) + string(storeBody)
	for _, secret := range []string{"super-secret-password-12345", "sk_live_1234567890abcdef"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("secret leaked to report/store: %s", secret)
		}
	}
}

func currentFileForTest() string {
	wd, _ := os.Getwd()
	return filepath.Join(wd, "reviewer_test.go")
}
