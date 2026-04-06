package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/utils"
)

const (
	defaultGitHubBaseURL    = "https://api.github.com"
	defaultGitHubTimeout    = 20 * time.Second
	defaultGitHubAPIVersion = "2022-11-28"
	defaultGitHubListLimit  = int64(10)
	defaultGitHubMaxChars   = int64(12000)
	maxGitHubListLimit      = int64(100)
	maxGitHubContentChars   = int64(40000)
	maxGitHubLogsZipBytes   = int64(15 * 1024 * 1024)
	gitHubToolUserAgent     = "picoclaw-github-tool/1.0"
)

type GitHubTool struct {
	client     *http.Client
	baseURL    string
	apiVersion string
	token      string
}

type gitHubUser struct {
	Login             string `json:"login"`
	Name              string `json:"name"`
	HTMLURL           string `json:"html_url"`
	Bio               string `json:"bio"`
	PublicRepos       int    `json:"public_repos"`
	TotalPrivateRepos int    `json:"total_private_repos"`
	Followers         int    `json:"followers"`
	Following         int    `json:"following"`
}

type gitHubRepo struct {
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	Fork          bool   `json:"fork"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Language      string `json:"language"`
	UpdatedAt     string `json:"updated_at"`
	PushedAt      string `json:"pushed_at"`
	OpenIssues    int    `json:"open_issues_count"`
	Watchers      int    `json:"watchers_count"`
	Stargazers    int    `json:"stargazers_count"`
	Forks         int    `json:"forks_count"`
}

type gitHubCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
			Date string `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}

type gitHubBranch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

type gitHubContent struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	Size        int    `json:"size"`
	Encoding    string `json:"encoding"`
	Content     string `json:"content"`
	SHA         string `json:"sha"`
	HTMLURL     string `json:"html_url"`
	DownloadURL string `json:"download_url"`
}

type gitHubWorkflowRun struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	DisplayTitle string `json:"display_title"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	Event        string `json:"event"`
	HeadBranch   string `json:"head_branch"`
	HeadSHA      string `json:"head_sha"`
	HTMLURL      string `json:"html_url"`
	UpdatedAt    string `json:"updated_at"`
}

type gitHubWorkflowRunsResponse struct {
	WorkflowRuns []gitHubWorkflowRun `json:"workflow_runs"`
}

func NewGitHubTool(token, baseURL, proxy string, timeoutSeconds int) (*GitHubTool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_MCP_PAT"))
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	if token == "" {
		return nil, fmt.Errorf("github token is required")
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultGitHubTimeout
	}

	client, err := utils.CreateHTTPClient(proxy, timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub HTTP client: %w", err)
	}

	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultGitHubBaseURL
	}

	return &GitHubTool{
		client:     client,
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiVersion: defaultGitHubAPIVersion,
		token:      token,
	}, nil
}

func (t *GitHubTool) Name() string {
	return "github"
}

func (t *GitHubTool) Description() string {
	return "Inspect GitHub via the REST API. Supports account lookup, repo listing, repo summaries, branches, directory listings, file reads, workflow runs, and workflow run logs."
}

func (t *GitHubTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"me",
					"my_repos",
					"repo_summary",
					"list_branches",
					"list_directory",
					"get_file",
					"list_workflow_runs",
					"get_workflow_run_logs",
				},
				"description": "GitHub action to run",
			},
			"repo": map[string]any{
				"type":        "string",
				"description": "Repository in owner/repo format",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Repository file or directory path for content actions",
			},
			"ref": map[string]any{
				"type":        "string",
				"description": "Branch, tag, or commit for content actions",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return",
			},
			"visibility": map[string]any{
				"type":        "string",
				"enum":        []string{"all", "public", "private"},
				"description": "Visibility filter for my_repos",
			},
			"affiliation": map[string]any{
				"type":        "string",
				"description": "Affiliation filter for my_repos, for example owner,collaborator,organization_member",
			},
			"branch": map[string]any{
				"type":        "string",
				"description": "Branch filter for workflow runs",
			},
			"status": map[string]any{
				"type":        "string",
				"description": "Workflow status filter, for example queued, in_progress, completed, success, failure",
			},
			"event": map[string]any{
				"type":        "string",
				"description": "Workflow event filter, for example push or pull_request",
			},
			"run_id": map[string]any{
				"type":        "integer",
				"description": "Workflow run ID for log retrieval",
			},
			"max_chars": map[string]any{
				"type":        "integer",
				"description": "Maximum characters to return for file content or workflow logs",
			},
		},
		"required": []string{"action"},
	}
}

func (t *GitHubTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action, _ := args["action"].(string)
	if action == "" {
		return ErrorResult("action is required")
	}

	var (
		output string
		err    error
	)

	switch action {
	case "me":
		output, err = t.me(ctx)
	case "my_repos":
		output, err = t.myRepos(ctx, args)
	case "repo_summary":
		output, err = t.repoSummary(ctx, args)
	case "list_branches":
		output, err = t.listBranches(ctx, args)
	case "list_directory":
		output, err = t.listDirectory(ctx, args)
	case "get_file":
		output, err = t.getFile(ctx, args)
	case "list_workflow_runs":
		output, err = t.listWorkflowRuns(ctx, args)
	case "get_workflow_run_logs":
		output, err = t.getWorkflowRunLogs(ctx, args)
	default:
		return ErrorResult(fmt.Sprintf("unknown github action: %s", action))
	}

	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	return SilentResult(output)
}

func (t *GitHubTool) me(ctx context.Context) (string, error) {
	var user gitHubUser
	if err := t.getJSON(ctx, "/user", nil, &user); err != nil {
		return "", err
	}

	lines := []string{
		fmt.Sprintf("Authenticated GitHub user: %s", user.Login),
	}
	if strings.TrimSpace(user.Name) != "" {
		lines = append(lines, fmt.Sprintf("Name: %s", user.Name))
	}
	if strings.TrimSpace(user.HTMLURL) != "" {
		lines = append(lines, fmt.Sprintf("Profile: %s", user.HTMLURL))
	}
	if strings.TrimSpace(user.Bio) != "" {
		lines = append(lines, fmt.Sprintf("Bio: %s", user.Bio))
	}
	lines = append(lines,
		fmt.Sprintf("Public repos: %d", user.PublicRepos),
		fmt.Sprintf("Private repos visible to token: %d", user.TotalPrivateRepos),
		fmt.Sprintf("Followers: %d", user.Followers),
		fmt.Sprintf("Following: %d", user.Following),
	)
	return strings.Join(lines, "\n"), nil
}

func (t *GitHubTool) myRepos(ctx context.Context, args map[string]any) (string, error) {
	limit, err := getBoundedIntArg(args, "limit", defaultGitHubListLimit, 1, maxGitHubListLimit)
	if err != nil {
		return "", err
	}
	visibility, err := stringArg(args, "visibility", "")
	if err != nil {
		return "", err
	}
	affiliation, err := stringArg(args, "affiliation", "")
	if err != nil {
		return "", err
	}

	query := url.Values{}
	query.Set("per_page", fmt.Sprintf("%d", limit))
	query.Set("sort", "updated")
	query.Set("direction", "desc")
	if visibility != "" {
		query.Set("visibility", visibility)
	}
	if affiliation != "" {
		query.Set("affiliation", affiliation)
	}

	var repos []gitHubRepo
	if err := t.getJSON(ctx, "/user/repos", query, &repos); err != nil {
		return "", err
	}
	if len(repos) == 0 {
		return "No repositories matched the current filters.", nil
	}

	lines := []string{fmt.Sprintf("Repositories (%d):", len(repos))}
	for _, repo := range repos {
		visibilityLabel := "public"
		if repo.Private {
			visibilityLabel = "private"
		}
		extras := []string{visibilityLabel}
		if repo.Archived {
			extras = append(extras, "archived")
		}
		if repo.Fork {
			extras = append(extras, "fork")
		}
		meta := strings.Join(extras, ", ")
		if repo.Language != "" {
			meta += ", " + repo.Language
		}
		lines = append(lines, fmt.Sprintf("- %s [%s]", repo.FullName, meta))
		if repo.Description != "" {
			lines = append(lines, "  "+repo.Description)
		}
		lines = append(lines, fmt.Sprintf("  default=%s updated=%s", repo.DefaultBranch, repo.UpdatedAt))
	}
	return strings.Join(lines, "\n"), nil
}

func (t *GitHubTool) repoSummary(ctx context.Context, args map[string]any) (string, error) {
	owner, repoName, err := repoArg(args)
	if err != nil {
		return "", err
	}

	var repo gitHubRepo
	if err := t.getJSON(ctx, fmt.Sprintf("/repos/%s/%s", owner, repoName), nil, &repo); err != nil {
		return "", err
	}

	languages := map[string]int{}
	_ = t.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/languages", owner, repoName), nil, &languages)

	var commits []gitHubCommit
	commitsQuery := url.Values{}
	commitsQuery.Set("per_page", "5")
	_ = t.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/commits", owner, repoName), commitsQuery, &commits)

	visibilityLabel := "public"
	if repo.Private {
		visibilityLabel = "private"
	}

	lines := []string{
		fmt.Sprintf("Repository: %s", repo.FullName),
		fmt.Sprintf("URL: %s", repo.HTMLURL),
		fmt.Sprintf("Visibility: %s", visibilityLabel),
		fmt.Sprintf("Default branch: %s", repo.DefaultBranch),
		fmt.Sprintf("Archived: %t", repo.Archived),
		fmt.Sprintf("Open issues: %d", repo.OpenIssues),
		fmt.Sprintf("Stars: %d", repo.Stargazers),
		fmt.Sprintf("Forks: %d", repo.Forks),
		fmt.Sprintf("Watchers: %d", repo.Watchers),
		fmt.Sprintf("Last push: %s", repo.PushedAt),
	}
	if repo.Description != "" {
		lines = append(lines, fmt.Sprintf("Description: %s", repo.Description))
	}
	if len(languages) > 0 {
		lines = append(lines, "Languages: "+formatLanguageBreakdown(languages))
	}
	if len(commits) > 0 {
		lines = append(lines, "Recent commits:")
		for _, commit := range commits {
			subject := strings.TrimSpace(strings.Split(commit.Commit.Message, "\n")[0])
			lines = append(lines, fmt.Sprintf("- %s %s (%s, %s)",
				shortSHA(commit.SHA), subject, commit.Commit.Author.Name, commit.Commit.Author.Date))
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (t *GitHubTool) listBranches(ctx context.Context, args map[string]any) (string, error) {
	owner, repoName, err := repoArg(args)
	if err != nil {
		return "", err
	}
	limit, err := getBoundedIntArg(args, "limit", defaultGitHubListLimit, 1, maxGitHubListLimit)
	if err != nil {
		return "", err
	}

	query := url.Values{}
	query.Set("per_page", fmt.Sprintf("%d", limit))

	var branches []gitHubBranch
	if err := t.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/branches", owner, repoName), query, &branches); err != nil {
		return "", err
	}
	if len(branches) == 0 {
		return fmt.Sprintf("No branches returned for %s/%s.", owner, repoName), nil
	}

	lines := []string{fmt.Sprintf("Branches for %s/%s:", owner, repoName)}
	for _, branch := range branches {
		protected := "unprotected"
		if branch.Protected {
			protected = "protected"
		}
		lines = append(lines, fmt.Sprintf("- %s [%s] %s", branch.Name, protected, shortSHA(branch.Commit.SHA)))
	}
	return strings.Join(lines, "\n"), nil
}

func (t *GitHubTool) listDirectory(ctx context.Context, args map[string]any) (string, error) {
	owner, repoName, err := repoArg(args)
	if err != nil {
		return "", err
	}
	ref, err := stringArg(args, "ref", "")
	if err != nil {
		return "", err
	}
	pathArg, err := stringArg(args, "path", "")
	if err != nil {
		return "", err
	}

	body, err := t.getContentRaw(ctx, owner, repoName, pathArg, ref)
	if err != nil {
		return "", err
	}

	var items []gitHubContent
	if err := json.Unmarshal(body, &items); err != nil {
		var file gitHubContent
		if err2 := json.Unmarshal(body, &file); err2 == nil && file.Type == "file" {
			return "", fmt.Errorf("%s is a file; use get_file instead", displayRepoPath(pathArg))
		}
		return "", fmt.Errorf("unexpected GitHub contents response: %w", err)
	}
	if len(items) == 0 {
		return fmt.Sprintf("Directory %s is empty.", displayRepoPath(pathArg)), nil
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Type != items[j].Type {
			return items[i].Type == "dir"
		}
		return items[i].Name < items[j].Name
	})

	lines := []string{fmt.Sprintf("Directory listing for %s/%s:%s", owner, repoName, formatRepoPathSuffix(pathArg))}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s %s (%d bytes)", item.Type, item.Path, item.Size))
	}
	return strings.Join(lines, "\n"), nil
}

func (t *GitHubTool) getFile(ctx context.Context, args map[string]any) (string, error) {
	owner, repoName, err := repoArg(args)
	if err != nil {
		return "", err
	}
	pathArg, err := stringArg(args, "path", "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(pathArg) == "" {
		return "", fmt.Errorf("path is required")
	}
	ref, err := stringArg(args, "ref", "")
	if err != nil {
		return "", err
	}
	maxChars, err := getBoundedIntArg(args, "max_chars", defaultGitHubMaxChars, 256, maxGitHubContentChars)
	if err != nil {
		return "", err
	}

	body, err := t.getContentRaw(ctx, owner, repoName, pathArg, ref)
	if err != nil {
		return "", err
	}

	var file gitHubContent
	if err := json.Unmarshal(body, &file); err != nil {
		return "", fmt.Errorf("failed to decode file response: %w", err)
	}
	if file.Type != "file" {
		return "", fmt.Errorf("%s is not a file", displayRepoPath(pathArg))
	}
	if file.Encoding != "base64" {
		return "", fmt.Errorf("unsupported file encoding %q for %s", file.Encoding, pathArg)
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("failed to decode file content: %w", err)
	}

	text := normalizeGitHubText(string(decoded))
	truncated := false
	if int64(len(text)) > maxChars {
		text = text[:maxChars]
		truncated = true
	}

	lines := []string{
		fmt.Sprintf("File: %s", file.Path),
		fmt.Sprintf("Repository: %s/%s", owner, repoName),
	}
	if ref != "" {
		lines = append(lines, fmt.Sprintf("Ref: %s", ref))
	}
	lines = append(lines, fmt.Sprintf("Size: %d bytes", file.Size))
	if truncated {
		lines = append(lines, fmt.Sprintf("Content truncated to %d characters.", maxChars))
	}
	lines = append(lines, "", text)
	return strings.Join(lines, "\n"), nil
}

func (t *GitHubTool) listWorkflowRuns(ctx context.Context, args map[string]any) (string, error) {
	owner, repoName, err := repoArg(args)
	if err != nil {
		return "", err
	}
	limit, err := getBoundedIntArg(args, "limit", defaultGitHubListLimit, 1, maxGitHubListLimit)
	if err != nil {
		return "", err
	}
	branch, err := stringArg(args, "branch", "")
	if err != nil {
		return "", err
	}
	status, err := stringArg(args, "status", "")
	if err != nil {
		return "", err
	}
	event, err := stringArg(args, "event", "")
	if err != nil {
		return "", err
	}

	query := url.Values{}
	query.Set("per_page", fmt.Sprintf("%d", limit))
	if branch != "" {
		query.Set("branch", branch)
	}
	if status != "" {
		query.Set("status", status)
	}
	if event != "" {
		query.Set("event", event)
	}

	var resp gitHubWorkflowRunsResponse
	if err := t.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/actions/runs", owner, repoName), query, &resp); err != nil {
		return "", err
	}
	if len(resp.WorkflowRuns) == 0 {
		return fmt.Sprintf("No workflow runs matched for %s/%s.", owner, repoName), nil
	}

	lines := []string{fmt.Sprintf("Workflow runs for %s/%s:", owner, repoName)}
	for _, run := range resp.WorkflowRuns {
		title := strings.TrimSpace(run.DisplayTitle)
		if title == "" {
			title = run.Name
		}
		statusText := run.Status
		if run.Conclusion != "" {
			statusText += "/" + run.Conclusion
		}
		lines = append(lines, fmt.Sprintf("- #%d %s [%s] branch=%s sha=%s event=%s updated=%s",
			run.ID, title, statusText, run.HeadBranch, shortSHA(run.HeadSHA), run.Event, run.UpdatedAt))
		if run.HTMLURL != "" {
			lines = append(lines, "  "+run.HTMLURL)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (t *GitHubTool) getWorkflowRunLogs(ctx context.Context, args map[string]any) (string, error) {
	owner, repoName, err := repoArg(args)
	if err != nil {
		return "", err
	}
	runID, err := getBoundedIntArg(args, "run_id", 0, 1, 1<<62)
	if err != nil {
		return "", err
	}
	maxChars, err := getBoundedIntArg(args, "max_chars", defaultGitHubMaxChars, 512, maxGitHubContentChars)
	if err != nil {
		return "", err
	}

	body, err := t.getBytes(ctx, fmt.Sprintf("/repos/%s/%s/actions/runs/%d/logs", owner, repoName, runID), nil, maxGitHubLogsZipBytes)
	if err != nil {
		return "", err
	}

	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", fmt.Errorf("failed to read workflow logs archive: %w", err)
	}
	if len(reader.File) == 0 {
		return "", fmt.Errorf("workflow logs archive is empty")
	}

	sort.Slice(reader.File, func(i, j int) bool {
		return reader.File[i].Name < reader.File[j].Name
	})

	var b strings.Builder
	fmt.Fprintf(&b, "Workflow logs for %s/%s run #%d\n", owner, repoName, runID)
	remaining := maxChars - int64(b.Len())
	if remaining <= 0 {
		return b.String(), nil
	}

	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if remaining <= 0 {
			break
		}

		rc, err := file.Open()
		if err != nil {
			return "", fmt.Errorf("failed to open workflow log %s: %w", file.Name, err)
		}
		entryBytes, readErr := io.ReadAll(io.LimitReader(rc, remaining+1))
		rc.Close()
		if readErr != nil {
			return "", fmt.Errorf("failed to read workflow log %s: %w", file.Name, readErr)
		}

		entryText := normalizeGitHubText(string(entryBytes))
		entryHeader := fmt.Sprintf("\n=== %s ===\n", file.Name)
		if int64(len(entryHeader)) >= remaining {
			break
		}
		b.WriteString(entryHeader)
		remaining = maxChars - int64(b.Len())

		truncated := int64(len(entryText)) > remaining
		if truncated {
			entryText = entryText[:remaining]
		}
		b.WriteString(entryText)
		if truncated {
			b.WriteString("\n[truncated]\n")
		}
		remaining = maxChars - int64(b.Len())
	}

	if maxChars-int64(b.Len()) <= 0 {
		b.WriteString("\n[overall log output truncated]\n")
	}
	return b.String(), nil
}

func (t *GitHubTool) getJSON(ctx context.Context, apiPath string, query url.Values, dst any) error {
	body, err := t.getBytes(ctx, apiPath, query, 0)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("failed to decode GitHub response: %w", err)
	}
	return nil
}

func (t *GitHubTool) getContentRaw(ctx context.Context, owner, repoName, contentPath, ref string) ([]byte, error) {
	relPath := fmt.Sprintf("/repos/%s/%s/contents", owner, repoName)
	if trimmed := strings.Trim(contentPath, "/"); trimmed != "" {
		escaped := make([]string, 0, len(strings.Split(trimmed, "/")))
		for _, part := range strings.Split(trimmed, "/") {
			escaped = append(escaped, url.PathEscape(part))
		}
		relPath += "/" + strings.Join(escaped, "/")
	}
	query := url.Values{}
	if ref != "" {
		query.Set("ref", ref)
	}
	return t.getBytes(ctx, relPath, query, 0)
}

func (t *GitHubTool) getBytes(ctx context.Context, apiPath string, query url.Values, maxBytes int64) ([]byte, error) {
	req, err := t.newRequest(ctx, apiPath, query)
	if err != nil {
		return nil, err
	}

	resp, err := utils.DoRequestWithRetry(t.client, req)
	if err != nil {
		return nil, fmt.Errorf("GitHub request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GitHub API %s returned %d: %s", apiPath, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	reader := io.Reader(resp.Body)
	if maxBytes > 0 {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read GitHub response: %w", err)
	}
	if maxBytes > 0 && int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("GitHub response exceeded %d bytes", maxBytes)
	}
	return body, nil
}

func (t *GitHubTool) newRequest(ctx context.Context, apiPath string, query url.Values) (*http.Request, error) {
	fullURL := t.baseURL + apiPath
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("User-Agent", gitHubToolUserAgent)
	req.Header.Set("X-GitHub-Api-Version", t.apiVersion)
	return req, nil
}

func repoArg(args map[string]any) (string, string, error) {
	repo, err := stringArg(args, "repo", "")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(repo) == "" {
		return "", "", fmt.Errorf("repo is required")
	}
	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo must be in owner/repo format")
	}
	return parts[0], parts[1], nil
}

func getBoundedIntArg(args map[string]any, key string, defaultVal, minVal, maxVal int64) (int64, error) {
	value, err := getInt64Arg(args, key, defaultVal)
	if err != nil {
		return 0, err
	}
	if value < minVal {
		return 0, fmt.Errorf("%s must be at least %d", key, minVal)
	}
	if value > maxVal {
		return maxVal, nil
	}
	return value, nil
}

func formatLanguageBreakdown(languages map[string]int) string {
	type item struct {
		Name  string
		Bytes int
	}
	items := make([]item, 0, len(languages))
	total := 0
	for name, count := range languages {
		items = append(items, item{Name: name, Bytes: count})
		total += count
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Bytes > items[j].Bytes
	})

	parts := make([]string, 0, len(items))
	for _, item := range items {
		if total == 0 {
			parts = append(parts, item.Name)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %.1f%%", item.Name, (float64(item.Bytes)/float64(total))*100))
	}
	return strings.Join(parts, ", ")
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func displayRepoPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "/"
	}
	return path
}

func formatRepoPathSuffix(path string) string {
	if strings.TrimSpace(path) == "" {
		return "/"
	}
	return ":" + path
}

func normalizeGitHubText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.TrimSpace(text)
}
