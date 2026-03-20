//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main validates the repository owner review policy for pull requests.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

const (
	defaultCodeownersPath = ".github/CODEOWNERS"
	defaultGitHubAPIURL   = "https://api.github.com"
	perPage               = 100
)

var githubAPIClient = &http.Client{Timeout: 30 * time.Second}

type config struct {
	APIURL         string
	CodeownersPath string
	EventPath      string
	Repository     string
	Token          string
}

type pullRequestEvent struct {
	PullRequest pullRequestPayload `json:"pull_request"`
	Repository  repositoryPayload  `json:"repository"`
}

type pullRequestPayload struct {
	Number int             `json:"number"`
	User   userPayload     `json:"user"`
	Base   pullRequestBase `json:"base"`
}

type pullRequestBase struct {
	Ref  string            `json:"ref"`
	SHA  string            `json:"sha"`
	Repo repositoryPayload `json:"repo"`
}

type repositoryPayload struct {
	FullName string `json:"full_name"`
}

type userPayload struct {
	Login string `json:"login"`
}

type pullRequestFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
}

type pullRequestReview struct {
	ID          int64       `json:"id"`
	State       string      `json:"state"`
	SubmittedAt *time.Time  `json:"submitted_at"`
	User        userPayload `json:"user"`
}

type reviewerState struct {
	ID          int64
	State       string
	SubmittedAt time.Time
}

type repositoryContentPayload struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type codeOwnerRule struct {
	Pattern string
	Owners  []string
}

type fileRequirement struct {
	Path   string
	Owners []string
}

type evaluationResult struct {
	Author            string
	ChangedPaths      []string
	Approvers         []string
	RequiredOwners    []string
	MatchingApprovers []string
	ExternalFiles     []fileRequirement
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		failf("Unable to load configuration: %v", err)
	}
	event, err := loadEvent(cfg.EventPath)
	if err != nil {
		failf("Unable to load GitHub event payload: %v", err)
	}
	repository := event.PullRequest.Base.Repo.FullName
	if repository == "" {
		repository = event.Repository.FullName
	}
	if repository == "" {
		repository = cfg.Repository
	}
	if repository == "" {
		failf("Unable to determine repository full name from event payload or environment.")
	}
	rules, err := loadCodeownersRules(cfg, repository, codeownersRef(event.PullRequest.Base))
	if err != nil {
		failf("Unable to load CODEOWNERS rules: %v", err)
	}
	files, err := fetchPullRequestFiles(cfg, repository, event.PullRequest.Number)
	if err != nil {
		failf("Unable to fetch pull request files: %v", err)
	}
	reviews, err := fetchPullRequestReviews(cfg, repository, event.PullRequest.Number)
	if err != nil {
		failf("Unable to fetch pull request reviews: %v", err)
	}
	changedPaths := collectChangedPaths(files)
	approvers := latestApprovedReviewers(reviews, event.PullRequest.User.Login)
	result, err := evaluatePolicy(rules, event.PullRequest.User.Login, changedPaths, approvers)
	if err != nil {
		failf("Unable to evaluate owner review policy: %v", err)
	}
	printEvaluation(result)
	if !isSatisfied(result) {
		failf("Owner review policy is not satisfied.")
	}
	fmt.Println("Owner review policy is satisfied.")
}

func loadConfig() (config, error) {
	cfg := config{
		APIURL:         strings.TrimSpace(os.Getenv("GITHUB_API_URL")),
		CodeownersPath: strings.TrimSpace(os.Getenv("CODEOWNERS_PATH")),
		EventPath:      strings.TrimSpace(os.Getenv("GITHUB_EVENT_PATH")),
		Repository:     strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY")),
		Token:          strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
	}
	if cfg.APIURL == "" {
		cfg.APIURL = defaultGitHubAPIURL
	}
	if cfg.EventPath == "" {
		return config{}, errors.New("GITHUB_EVENT_PATH is required")
	}
	if cfg.Token == "" {
		return config{}, errors.New("GITHUB_TOKEN is required")
	}
	return cfg, nil
}

func loadEvent(eventPath string) (pullRequestEvent, error) {
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return pullRequestEvent{}, err
	}
	var event pullRequestEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return pullRequestEvent{}, err
	}
	if event.PullRequest.Number == 0 {
		return pullRequestEvent{}, errors.New("event payload does not contain pull_request.number")
	}
	return event, nil
}

func loadCodeownersRules(cfg config, repository, ref string) ([]codeOwnerRule, error) {
	if cfg.CodeownersPath != "" {
		return parseCodeownersFile(cfg.CodeownersPath)
	}
	if strings.TrimSpace(repository) == "" {
		return nil, errors.New("base repository full name is required to fetch CODEOWNERS")
	}
	if strings.TrimSpace(ref) == "" {
		return nil, errors.New("pull request base ref is required to fetch CODEOWNERS")
	}
	// Always evaluate CODEOWNERS from the base revision so a pull request cannot relax its own review policy.
	data, err := fetchRepositoryFile(cfg, repository, defaultCodeownersPath, ref)
	if err != nil {
		return nil, err
	}
	return parseCodeownersContent(string(data))
}

func parseCodeownersFile(filePath string) ([]codeOwnerRule, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	return parseCodeownersContent(string(data))
}

func parseCodeownersContent(content string) ([]codeOwnerRule, error) {
	lines := strings.Split(content, "\n")
	rules := make([]codeOwnerRule, 0, len(lines))
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("invalid CODEOWNERS line: %q", rawLine)
		}
		owners := normalizeOwners(fields[1:])
		if len(owners) == 0 {
			return nil, fmt.Errorf("invalid CODEOWNERS line without owners: %q", rawLine)
		}
		rules = append(rules, codeOwnerRule{
			Pattern: fields[0],
			Owners:  owners,
		})
	}
	if len(rules) == 0 {
		return nil, errors.New("CODEOWNERS file does not contain any active rules")
	}
	return rules, nil
}

func fetchRepositoryFile(cfg config, repository, filePath, ref string) ([]byte, error) {
	normalizedPath := normalizeRepoPath(filePath)
	endpoint := fmt.Sprintf(
		"%s/repos/%s/contents/%s?ref=%s",
		strings.TrimRight(cfg.APIURL, "/"),
		repository,
		normalizedPath,
		url.QueryEscape(strings.TrimSpace(ref)),
	)
	var response repositoryContentPayload
	if err := fetchJSON(cfg.Token, endpoint, &response); err != nil {
		return nil, err
	}
	if strings.ToLower(strings.TrimSpace(response.Encoding)) != "base64" {
		return nil, fmt.Errorf("unsupported encoding %q for %s", response.Encoding, normalizedPath)
	}
	content := strings.ReplaceAll(response.Content, "\n", "")
	data, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, fmt.Errorf("unable to decode %s: %w", normalizedPath, err)
	}
	return data, nil
}

func fetchPullRequestFiles(cfg config, repository string, pullNumber int) ([]pullRequestFile, error) {
	files := []pullRequestFile{}
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d/files?per_page=%d&page=%d", strings.TrimRight(cfg.APIURL, "/"), repository, pullNumber, perPage, page)
		var pageFiles []pullRequestFile
		if err := fetchJSON(cfg.Token, endpoint, &pageFiles); err != nil {
			return nil, err
		}
		files = append(files, pageFiles...)
		if len(pageFiles) < perPage {
			break
		}
	}
	return files, nil
}

func fetchPullRequestReviews(cfg config, repository string, pullNumber int) ([]pullRequestReview, error) {
	reviews := []pullRequestReview{}
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d/reviews?per_page=%d&page=%d", strings.TrimRight(cfg.APIURL, "/"), repository, pullNumber, perPage, page)
		var pageReviews []pullRequestReview
		if err := fetchJSON(cfg.Token, endpoint, &pageReviews); err != nil {
			return nil, err
		}
		reviews = append(reviews, pageReviews...)
		if len(pageReviews) < perPage {
			break
		}
	}
	return reviews, nil
}

func fetchJSON(token, endpoint string, out any) error {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "trpc-agent-go-owner-review-policy")
	resp, err := githubAPIClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub API request failed with status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return err
	}
	return nil
}

func collectChangedPaths(files []pullRequestFile) []string {
	seen := map[string]struct{}{}
	paths := []string{}
	for _, file := range files {
		for _, candidate := range []string{file.PreviousFilename, file.Filename} {
			normalized := normalizeRepoPath(candidate)
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			paths = append(paths, normalized)
		}
	}
	sort.Strings(paths)
	return paths
}

func latestApprovedReviewers(reviews []pullRequestReview, author string) []string {
	authorLogin := normalizeLogin(author)
	orderedReviews := append([]pullRequestReview(nil), reviews...)
	sort.Slice(orderedReviews, func(i, j int) bool {
		return reviewIsNewer(reviewStateFromReview(orderedReviews[j]), reviewStateFromReview(orderedReviews[i]))
	})
	latestByReviewer := map[string]reviewerState{}
	for _, review := range orderedReviews {
		login := normalizeLogin(review.User.Login)
		if login == "" || login == authorLogin {
			continue
		}
		state := strings.ToUpper(strings.TrimSpace(review.State))
		if state == "" || state == "PENDING" {
			continue
		}
		current := reviewStateFromReview(review)
		if state == "COMMENTED" {
			// Comment-only reviews should not clear a prior approval state.
			continue
		}
		latestByReviewer[login] = current
	}
	approvers := []string{}
	for reviewer, state := range latestByReviewer {
		if state.State == "APPROVED" {
			approvers = append(approvers, reviewer)
		}
	}
	sort.Strings(approvers)
	return approvers
}

func reviewIsNewer(current, existing reviewerState) bool {
	if current.SubmittedAt.After(existing.SubmittedAt) {
		return true
	}
	if current.SubmittedAt.Before(existing.SubmittedAt) {
		return false
	}
	return current.ID > existing.ID
}

func reviewStateFromReview(review pullRequestReview) reviewerState {
	submittedAt := time.Time{}
	if review.SubmittedAt != nil {
		submittedAt = review.SubmittedAt.UTC()
	}
	return reviewerState{
		ID:          review.ID,
		State:       strings.ToUpper(strings.TrimSpace(review.State)),
		SubmittedAt: submittedAt,
	}
}

func codeownersRef(base pullRequestBase) string {
	if strings.TrimSpace(base.SHA) != "" {
		return strings.TrimSpace(base.SHA)
	}
	return strings.TrimSpace(base.Ref)
}

func evaluatePolicy(rules []codeOwnerRule, author string, changedPaths, approvers []string) (evaluationResult, error) {
	authorOwner := ownerTokenFromLogin(author)
	requiredOwners := map[string]struct{}{}
	matchingApprovers := map[string]struct{}{}
	externalFiles := []fileRequirement{}
	for _, changedPath := range uniqueSortedStrings(changedPaths) {
		owners := ownersForPath(rules, changedPath)
		if len(owners) == 0 {
			continue
		}
		if containsString(owners, authorOwner) {
			continue
		}
		fileOwners := make([]string, 0, len(owners))
		for _, owner := range owners {
			if owner == authorOwner {
				continue
			}
			if strings.Contains(owner, "/") {
				return evaluationResult{}, fmt.Errorf("team CODEOWNERS entry %q is not supported by this policy check", owner)
			}
			requiredOwners[owner] = struct{}{}
			fileOwners = append(fileOwners, owner)
		}
		if len(fileOwners) == 0 {
			continue
		}
		sort.Strings(fileOwners)
		externalFiles = append(externalFiles, fileRequirement{
			Path:   changedPath,
			Owners: fileOwners,
		})
	}
	for _, approver := range uniqueSortedStrings(approvers) {
		if _, ok := requiredOwners[ownerTokenFromLogin(approver)]; ok {
			matchingApprovers[approver] = struct{}{}
		}
	}
	sort.Slice(externalFiles, func(i, j int) bool {
		return externalFiles[i].Path < externalFiles[j].Path
	})
	return evaluationResult{
		Author:            normalizeLogin(author),
		ChangedPaths:      uniqueSortedStrings(changedPaths),
		Approvers:         uniqueSortedStrings(approvers),
		RequiredOwners:    mapKeys(requiredOwners),
		MatchingApprovers: mapKeys(matchingApprovers),
		ExternalFiles:     externalFiles,
	}, nil
}

func ownersForPath(rules []codeOwnerRule, repoPath string) []string {
	normalizedPath := normalizeRepoPath(repoPath)
	owners := []string{}
	for _, rule := range rules {
		if ruleMatches(rule.Pattern, normalizedPath) {
			owners = append([]string(nil), rule.Owners...)
		}
	}
	return owners
}

func ruleMatches(pattern, repoPath string) bool {
	normalizedPattern := strings.TrimSpace(pattern)
	normalizedPath := normalizeRepoPath(repoPath)
	if normalizedPattern == "" || normalizedPath == "" {
		return false
	}
	if normalizedPattern == "*" {
		return true
	}
	rooted := strings.HasPrefix(normalizedPattern, "/")
	trimmedPattern := strings.TrimPrefix(normalizedPattern, "/")
	if strings.HasSuffix(trimmedPattern, "/") {
		dirPattern := strings.TrimSuffix(trimmedPattern, "/")
		if rooted {
			return normalizedPath == dirPattern || strings.HasPrefix(normalizedPath, dirPattern+"/")
		}
		return normalizedPath == dirPattern || strings.HasPrefix(normalizedPath, dirPattern+"/") || strings.Contains(normalizedPath, "/"+dirPattern+"/")
	}
	patterns := []string{trimmedPattern}
	if strings.ContainsAny(trimmedPattern, "*?[") {
		if !rooted {
			patterns = append(patterns, "**/"+trimmedPattern)
		}
		for _, candidate := range patterns {
			matched, err := doublestar.PathMatch(candidate, normalizedPath)
			if err == nil && matched {
				return true
			}
		}
		return false
	}
	if rooted {
		return normalizedPath == trimmedPattern
	}
	return normalizedPath == trimmedPattern || strings.HasSuffix(normalizedPath, "/"+trimmedPattern)
}

func normalizeOwners(owners []string) []string {
	unique := map[string]struct{}{}
	normalized := []string{}
	for _, owner := range owners {
		value := strings.ToLower(strings.TrimSpace(owner))
		if value == "" {
			continue
		}
		if !strings.HasPrefix(value, "@") {
			value = "@" + value
		}
		if _, ok := unique[value]; ok {
			continue
		}
		unique[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

func normalizeLogin(login string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(login, "@")))
}

func ownerTokenFromLogin(login string) string {
	normalized := normalizeLogin(login)
	if normalized == "" {
		return ""
	}
	return "@" + normalized
}

func normalizeRepoPath(repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		return ""
	}
	cleaned := path.Clean(strings.ReplaceAll(repoPath, "\\", "/"))
	return strings.TrimPrefix(cleaned, "/")
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func uniqueSortedStrings(values []string) []string {
	unique := map[string]struct{}{}
	result := []string{}
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}
		if _, ok := unique[normalized]; ok {
			continue
		}
		unique[normalized] = struct{}{}
		result = append(result, normalized)
	}
	sort.Strings(result)
	return result
}

func mapKeys[K ~string](values map[K]struct{}) []K {
	keys := make([]K, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})
	return keys
}

func isSatisfied(result evaluationResult) bool {
	return len(result.RequiredOwners) == 0 || len(result.MatchingApprovers) > 0
}

func printEvaluation(result evaluationResult) {
	fmt.Printf("Pull request author: @%s\n", result.Author)
	fmt.Printf("Changed paths: %d\n", len(result.ChangedPaths))
	if len(result.Approvers) == 0 {
		fmt.Println("Current approvers: none")
	} else {
		fmt.Printf("Current approvers: %s\n", strings.Join(prefixAll(result.Approvers, "@"), ", "))
	}
	if len(result.ExternalFiles) == 0 {
		fmt.Println("Files requiring external owner review: none")
		return
	}
	fmt.Println("Files requiring external owner review:")
	for _, file := range result.ExternalFiles {
		fmt.Printf("- %s => %s\n", file.Path, strings.Join(file.Owners, ", "))
	}
	fmt.Printf("Eligible external owners: %s\n", strings.Join(result.RequiredOwners, ", "))
	if len(result.MatchingApprovers) == 0 {
		fmt.Println("Matching external owner approvers: none")
		return
	}
	fmt.Printf("Matching external owner approvers: %s\n", strings.Join(prefixAll(result.MatchingApprovers, "@"), ", "))
}

func prefixAll(values []string, prefix string) []string {
	prefixed := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			prefixed = append(prefixed, value)
			continue
		}
		prefixed = append(prefixed, prefix+value)
	}
	return prefixed
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "::error::"+format+"\n", args...)
	os.Exit(1)
}
