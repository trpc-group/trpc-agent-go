//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestRedactorDoesNotTrustEmbeddedPlaceholder(t *testing.T) {
	input := "password=" + RedactedValue + "hunter2"
	redacted, count := NewRedactor().RedactString(input)

	if count == 0 {
		t.Fatal("embedded placeholder suffix was not detected")
	}
	if redacted != "password="+RedactedValue {
		t.Fatalf("RedactString() = %q, want exact placeholder", redacted)
	}

	again, secondCount := NewRedactor().RedactString(redacted)
	if secondCount != 0 || again != redacted {
		t.Fatalf(
			"second redaction = %q, count=%d, want idempotent result",
			again,
			secondCount,
		)
	}
}

func TestRedactorIsIdempotentAcrossReportAndAuditBoundaries(t *testing.T) {
	redactor := NewRedactor()
	once, firstCount := redactor.RedactString("token=plain-secret")
	twice, secondCount := redactor.RedactString(once)
	if firstCount == 0 {
		t.Fatal("first redaction did not detect the secret")
	}
	if secondCount != 0 || twice != once {
		t.Fatalf("second redaction changed placeholder: count=%d once=%q twice=%q",
			secondCount, once, twice)
	}

	var output bytes.Buffer
	sink := NewJSONLAuditSink(&output)
	if err := sink.WriteAudit(context.Background(), AuditEvent{
		RuleID:   once,
		Evidence: once,
	}); err != nil {
		t.Fatalf("WriteAudit() error = %v", err)
	}
	var event AuditEvent
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if event.RuleID != once || event.Evidence != once {
		t.Fatalf("audit changed redaction placeholder: %+v", event)
	}
}

func TestRedactorConsumesEmbeddedPlaceholderWithPrefix(t *testing.T) {
	input := "password=x" + RedactedValue + "hunter2"
	redacted, count := NewRedactor().RedactString(input)

	if count == 0 {
		t.Fatal("embedded placeholder with prefix was not detected")
	}
	if redacted != "password="+RedactedValue {
		t.Fatalf("RedactString() = %q, want exact placeholder", redacted)
	}
}
