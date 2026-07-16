//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Guard is a tool.PermissionPolicy that scans tool calls before execution.
// It extracts the execution request from tool arguments, scans it with the
// Scanner, records an audit event, and returns the appropriate
// tool.PermissionDecision.
//
// Fail-closed: if extraction fails, the Guard denies the call.
type Guard struct {
	scanner     *Scanner
	auditWriter *AuditWriter
	auditMu     sync.Mutex
	auditFile   *os.File
	reportSink  func(Report)
	redactor    *Redactor
	extractors  map[string]Extractor
	closer      func()
}

// Compile-time check that Guard implements tool.PermissionPolicy.
var _ tool.PermissionPolicy = (*Guard)(nil)

// GuardOption configures a Guard. If the option encounters a configuration
// error it returns a non-nil error, which causes NewGuard to abort and
// return that error to the caller.
type GuardOption func(*Guard) error

// NewGuard creates a new Guard with the given options.
// By default it uses DefaultPolicy() and NewRedactor().
// Returns an error if any option reports a configuration error
// (e.g. policy file not found, audit file cannot be opened).
func NewGuard(opts ...GuardOption) (*Guard, error) {
	g := &Guard{
		scanner:    NewScanner(DefaultPolicy()),
		redactor:   NewRedactor(),
		extractors: make(map[string]Extractor),
		closer:     func() {},
	}
	// Copy default extractors.
	for k, v := range defaultExtractors {
		g.extractors[k] = v
	}
	for _, opt := range opts {
		if err := opt(g); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// WithPolicyFile configures the Guard to load policy from a YAML/JSON file.
// Returns a configuration error if the file cannot be loaded or parsed.
func WithPolicyFile(path string) GuardOption {
	return func(g *Guard) error {
		policy, err := LoadPolicyFile(path)
		if err != nil {
			return fmt.Errorf("load policy file %s: %w", path, err)
		}
		g.scanner = NewScanner(policy)
		return nil
	}
}

// WithPolicy configures the Guard with an explicit PolicyFile.
func WithPolicy(p PolicyFile) GuardOption {
	return func(g *Guard) error {
		g.scanner = NewScanner(p)
		return nil
	}
}

// WithAuditFile configures the Guard to write audit events to a file
// in append mode. If the file does not exist, it is created.
// Returns a configuration error if the file cannot be opened.
func WithAuditFile(path string) GuardOption {
	return func(g *Guard) error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open audit file %s: %w", path, err)
		}
		g.auditFile = f
		g.auditWriter = NewAuditWriter(f)
		g.closer = func() {
			f.Close()
		}
		return nil
	}
}

// WithAuditWriter configures the Guard to write audit events to an io.Writer.
func WithAuditWriter(w io.Writer) GuardOption {
	return func(g *Guard) error {
		g.auditWriter = NewAuditWriter(w)
		return nil
	}
}

// WithReportSink configures the Guard to send reports to the given callback
// after each scan.
func WithReportSink(fn func(Report)) GuardOption {
	return func(g *Guard) error {
		g.reportSink = fn
		return nil
	}
}

// WithExtractors configures the Guard with custom extractors.
// The map is copied; later calls do not mutate the Guard.
func WithExtractors(extractors map[string]Extractor) GuardOption {
	return func(g *Guard) error {
		for k, v := range extractors {
			g.extractors[k] = v
		}
		return nil
	}
}

// CheckToolPermission implements tool.PermissionPolicy.
// It extracts the execution request from tool arguments, scans it,
// records an audit event, and returns the appropriate PermissionDecision.
//
// Fail-closed: if extraction fails, the call is denied.
func (g *Guard) CheckToolPermission(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	start := time.Now()

	// Step 1: Extract request from tool arguments.
	execReq, err := extractRequest(req.ToolName, req.Arguments, g.extractors)
	if err != nil {
		// Fail-closed: deny on extraction failure.
		return tool.DenyPermission(fmt.Sprintf("safety guard: extraction failed: %v", err)), nil
	}

	// Step 2: Scan the request.
	scanInput := execReq.ToScanInput(req.ToolName)
	result := g.scanner.Scan(ctx, scanInput)
	duration := time.Since(start)

	// Step 3: Redact sensitive data.
	redacted := false
	if g.redactor != nil {
		result.Command = g.redactor.RedactString(result.Command)
		result.Findings = g.redactor.RedactFindings(result.Findings)
		redacted = true
	}

	// Step 4: Write audit event.
	if g.auditWriter != nil {
		event := auditEventFromScanResult(result, duration, redacted)
		_ = g.auditWriter.WriteEvent(event)
	}

	// Step 5: Send report to sink.
	if g.reportSink != nil {
		report := NewReport(result)
		if g.redactor != nil {
			g.redactor.RedactReport(&report)
		}
		g.reportSink(report)
	}

	// Step 6: Convert Decision to tool.PermissionDecision.
	return decisionFromTool(result.Decision), nil
}

// Close flushes pending audit events and closes files.
func (g *Guard) Close() error {
	g.auditMu.Lock()
	defer g.auditMu.Unlock()
	if g.closer != nil {
		g.closer()
	}
	return nil
}
