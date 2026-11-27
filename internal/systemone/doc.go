//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package systemone implements the POST /v1/systemone decision protocol used by
// TypeSafe Jev and compatible Laya servers using only the Go standard library.
// It does not implement chat, approval policy, or local model inference.
//
// # Provider configuration
//
// Jev callers configure https://api.typesafe.ai, an API key, and a model such as
// jev-1.13.0. A local Laya server may omit authentication and model selection;
// explicit checkpoint selection uses its own model names, such as multilingual.
// The client does not discover models or silently select a provider. The root
// URL can include a gateway prefix, but must not include the /v1/systemone route.
// The common request does not expose Laya-only inference controls or batching.
//
// # Decision evidence
//
// Confidence preserves the server's statistic. Jev and Laya compute it
// differently, and neither a confidence nor an act_probability establishes user
// authorization. Applications own calibration, thresholds, and approval rules.
// Response.Raw retains all server extensions, including Laya routing, abstention,
// action scores, and detailed truncation evidence. Usage.Truncated is nil when
// the server supplies no evidence, including strict Laya responses. A successful
// call does not establish that the model saw the complete state.
//
// # Request and response types
//
// State accepts caller-defined structs, maps, slices, and strings. The client
// uses encoding/json, including custom MarshalJSON methods, and requires a JSON
// string, object, or array. Callers do not need to implement an encoding method
// or supply raw JSON. State is never clipped, summarized, or split by this client.
//
// BinaryQuestion asks for P(true) and maps to the wire type "noul". ChoiceQuestion
// selects a named option; its ordered slice preserves wire presentation order.
// ScoreQuestion uses a slice of text criteria as ordinal levels starting at zero.
// All instructions must contain nonblank text. Descriptions and criteria are
// text-only, even when a provider supports richer JSON inputs. Score legends
// must also be strings; object or array legends are rejected, not stringified.
//
// Response.Binary, Response.Choice, and Response.Score return checked answers by
// ID without type assertions. They borrow objects from Response.Answers and do
// not re-decode Raw. ScoreAnswer.Levels pairs each description with its probability
// in ordinal order. The caller owns all response data; modifications require
// synchronization and do not update the other raw or typed representation.
//
// # Transport and validation
//
// The client sends each request once. Retries, provider fallback, and inference
// cancellation on the remote server are not guaranteed. Caller contexts cancel
// the HTTP operation; a client timeout defaults to 30 seconds. Redirects are
// refused and response bodies are limited to 8 MiB. No environment variables are
// read, and the client neither logs requests nor closes shared transports.
//
// Answers must match all requested IDs, types, and option/level sets. Missing or
// null required numeric fields are errors, while zero is valid. Distributions
// tolerate four-decimal rounding. Unknown extra response fields are preserved in
// Raw, but unknown answer types are errors. Usage counts may be absent/null.
//
// Protocol references: https://api.typesafe.ai/openapi.json (version 0.2.0) and
// https://github.com/NandhaKishorM/laya/blob/2e4d9c87e8b1621deb344eac7de5c7258f32f849/laya/serve.py.
// The testdata fixtures are synthetic contract samples, not live model outputs.
package systemone
