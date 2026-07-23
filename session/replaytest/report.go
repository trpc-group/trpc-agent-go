//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"fmt"
	"io"
)

// WriteReport validates and writes an indented JSON report.
func WriteReport(writer io.Writer, report Report) error {
	if writer == nil {
		return fmt.Errorf("replaytest: report writer is nil")
	}
	if err := report.Validate(); err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("replaytest: encode report: %w", err)
	}
	return nil
}
