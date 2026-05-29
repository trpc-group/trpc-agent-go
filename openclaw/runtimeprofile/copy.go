//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runtimeprofile

func cloneProfile(profile Profile) Profile {
	profile.Tools.Include = copyStrings(profile.Tools.Include)
	profile.Tools.Exclude = copyStrings(profile.Tools.Exclude)
	profile.Tools.ExecutionInclude = copyStrings(
		profile.Tools.ExecutionInclude,
	)
	profile.Tools.ExecutionExclude = copyStrings(
		profile.Tools.ExecutionExclude,
	)
	profile.Tools.ToolSets = copyStrings(profile.Tools.ToolSets)
	profile.Tools.CredentialRefs = copyStringMap(
		profile.Tools.CredentialRefs,
	)
	profile.Knowledge.Indexes = copyStrings(profile.Knowledge.Indexes)
	profile.Knowledge.Filter = copyAnyMap(profile.Knowledge.Filter)
	profile.Workspace.AllowedRoots = copyStrings(
		profile.Workspace.AllowedRoots,
	)
	profile.Credentials.AllowedRefs = copyStrings(
		profile.Credentials.AllowedRefs,
	)
	profile.Skills.Include = copyStrings(profile.Skills.Include)
	profile.Skills.Exclude = copyStrings(profile.Skills.Exclude)
	profile.Skills.Roots = copyStrings(profile.Skills.Roots)
	profile.State = copyAnyMap(profile.State)
	profile.ExtraModel = copyAnyMap(profile.ExtraModel)
	return profile
}

func cloneSelectors(selectors []Selector) []Selector {
	if len(selectors) == 0 {
		return nil
	}
	copied := make([]Selector, len(selectors))
	for i, selector := range selectors {
		copied[i] = selector
		copied[i].Channels = copyStrings(selector.Channels)
		copied[i].Tenants = copyStrings(selector.Tenants)
		copied[i].Users = copyStrings(selector.Users)
		copied[i].Sessions = copyStrings(selector.Sessions)
	}
	return copied
}

func copyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	copied := make([]string, len(values))
	copy(copied, values)
	return copied
}

func copyAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]any, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}
