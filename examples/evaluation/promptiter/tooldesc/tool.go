//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newTravelTools(travelLookupToolDescription string) []tool.Tool {
	statusTool := function.NewFunctionTool(
		getFlightStatus,
		function.WithName("lookup_record"),
		function.WithDescription(travelLookupToolDescription),
	)
	return []tool.Tool{statusTool}
}

type flightStatusArgs struct {
	Query string `json:"query" jsonschema:"description=Record key to look up,required"`
}

type recordLookupResult struct {
	RecordID  string `json:"recordId" jsonschema:"description=Resolved record identifier"`
	State     string `json:"state" jsonschema:"description=Primary record state"`
	Minutes   int    `json:"minutes" jsonschema:"description=Relevant minute value"`
	Location  string `json:"location" jsonschema:"description=Relevant location code"`
	Scheduled string `json:"scheduled" jsonschema:"description=Scheduled local time"`
	Updated   string `json:"updated" jsonschema:"description=Updated local time"`
}

func getFlightStatus(_ context.Context, args flightStatusArgs) (recordLookupResult, error) {
	switch args.Query {
	case "TR123":
		return recordLookupResult{RecordID: "TR123", State: "delayed", Minutes: 35, Location: "B12", Scheduled: "10:10", Updated: "10:45"}, nil
	case "TR456":
		return recordLookupResult{RecordID: "TR456", State: "delayed", Minutes: 15, Location: "A07", Scheduled: "12:30", Updated: "12:45"}, nil
	case "TR789":
		return recordLookupResult{RecordID: "TR789", State: "cancelled", Minutes: 0, Location: "", Scheduled: "18:00", Updated: ""}, nil
	case "TR321":
		return recordLookupResult{RecordID: "TR321", State: "delayed", Minutes: 20, Location: "C03", Scheduled: "14:20", Updated: "14:40"}, nil
	case "TR654":
		return recordLookupResult{RecordID: "TR654", State: "boarding", Minutes: 0, Location: "D18", Scheduled: "16:05", Updated: "16:05"}, nil
	default:
		return recordLookupResult{RecordID: args.Query, State: "unknown", Minutes: 0, Location: "", Scheduled: "", Updated: ""}, nil
	}
}
