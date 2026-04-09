//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package docling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/extractor"
)

type convertOptions struct {
	ToFormats       []string     `json:"to_formats,omitempty"`
	ImageExportMode ImageRefMode `json:"image_export_mode,omitempty"`
	DoOCR           *bool        `json:"do_ocr,omitempty"`
}

type convertDocumentResponse struct {
	Document exportDocumentResponse `json:"document"`
	Status   string                 `json:"status"`
	Errors   []struct {
		ErrorMessage string `json:"error_message"`
	} `json:"errors"`
}

type exportDocumentResponse struct {
	Filename       string `json:"filename"`
	MdContent      string `json:"md_content"`
	TextContent    string `json:"text_content"`
	HTMLContent    string `json:"html_content"`
	DocTagsContent string `json:"doctags_content"`
}

func buildConvertOptions(extOpts options, eopts *extractor.Options) convertOptions {
	opts := convertOptions{
		ToFormats:       []string{doclingOutputFormat(eopts.OutputFormat)},
		ImageExportMode: extOpts.imageRefMode,
	}
	if !extOpts.ocrEnabled {
		disabled := false
		opts.DoOCR = &disabled
	}
	return opts
}

func doclingOutputFormat(outputFormat string) string {
	if outputFormat == extractor.FormatText {
		return "text"
	}
	return "md"
}

func decodeConvertResponse(resp *http.Response) (*convertDocumentResponse, error) {
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("docling returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var convResp convertDocumentResponse
	if err := json.NewDecoder(resp.Body).Decode(&convResp); err != nil {
		return nil, fmt.Errorf("failed to decode docling response: %w", err)
	}

	if convResp.Status != "" && convResp.Status != "success" {
		errMsg := convResp.Status
		if len(convResp.Errors) > 0 {
			errMsg = convResp.Errors[0].ErrorMessage
		}
		return nil, fmt.Errorf("docling conversion failed: %s", errMsg)
	}

	return &convResp, nil
}

func toExtractorResult(convResp *convertDocumentResponse, outputFormat string) (*extractor.Result, error) {
	content, format := pickOutputContent(convResp.Document, outputFormat)
	if content == "" {
		return nil, fmt.Errorf("docling returned empty content")
	}
	return &extractor.Result{
		Reader: strings.NewReader(content),
		Format: format,
	}, nil
}

func pickOutputContent(doc exportDocumentResponse, outputFormat string) (string, string) {
	if outputFormat == extractor.FormatText {
		if doc.TextContent != "" {
			return doc.TextContent, extractor.FormatText
		}
		if doc.MdContent != "" {
			return doc.MdContent, extractor.FormatMarkdown
		}
		return "", extractor.FormatText
	}
	if doc.MdContent != "" {
		return doc.MdContent, extractor.FormatMarkdown
	}
	if doc.TextContent != "" {
		return doc.TextContent, extractor.FormatText
	}
	return "", extractor.FormatMarkdown
}

func writeFileRequest(writer *multipart.Writer, r io.Reader, opts convertOptions) error {
	part, err := writer.CreateFormFile("files", "document")
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err = io.Copy(part, r); err != nil {
		return fmt.Errorf("failed to copy data to form: %w", err)
	}
	if err = writer.WriteField("target_type", "inbody"); err != nil {
		return fmt.Errorf("failed to write target_type field: %w", err)
	}
	for _, format := range opts.ToFormats {
		if err = writer.WriteField("to_formats", format); err != nil {
			return fmt.Errorf("failed to write to_formats field: %w", err)
		}
	}
	if opts.ImageExportMode != "" {
		if err = writer.WriteField("image_export_mode", string(opts.ImageExportMode)); err != nil {
			return fmt.Errorf("failed to write image_export_mode field: %w", err)
		}
	}
	if opts.DoOCR != nil {
		if err = writer.WriteField("do_ocr", fmt.Sprintf("%t", *opts.DoOCR)); err != nil {
			return fmt.Errorf("failed to write do_ocr field: %w", err)
		}
	}
	return nil
}

func (e *Extractor) doFileConvert(ctx context.Context, r io.Reader, eopts *extractor.Options) (*extractor.Result, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	if err := writeFileRequest(writer, r, buildConvertOptions(e.opts, eopts)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := strings.TrimRight(e.opts.endpoint, "/") + convertFilePath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	resp, err := e.opts.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docling request failed: %w", err)
	}
	defer resp.Body.Close()

	convResp, err := decodeConvertResponse(resp)
	if err != nil {
		return nil, err
	}
	return toExtractorResult(convResp, eopts.OutputFormat)
}
