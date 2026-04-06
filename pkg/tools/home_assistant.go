package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	defaultHomeAssistantBaseURL = "http://127.0.0.1:8123"
	defaultHomeAssistantTimeout = 15 * time.Second
	defaultHAEntityLimit        = int64(25)
	maxHAEntityLimit            = int64(100)
	haToolUserAgent             = "picoclaw-home-assistant-tool/1.0"
)

type HomeAssistantTool struct {
	client  *http.Client
	baseURL string
	token   string
}

type homeAssistantState struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged string         `json:"last_changed"`
	LastUpdated string         `json:"last_updated"`
}

func NewHomeAssistantTool(baseURL, token string, timeoutSeconds int) (*HomeAssistantTool, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultHomeAssistantBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("valid Home Assistant base_url is required")
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("home assistant token is required")
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultHomeAssistantTimeout
	}

	return &HomeAssistantTool{
		client:  &http.Client{Timeout: timeout},
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
	}, nil
}

func (t *HomeAssistantTool) Name() string {
	return "home_assistant"
}

func (t *HomeAssistantTool) Description() string {
	return "Control and inspect Home Assistant over its REST API. Supports API status, entity listing, entity state reads, and service calls."
}

func (t *HomeAssistantTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "list_entities", "get_state", "call_service"},
				"description": "Home Assistant action to run",
			},
			"entity_id": map[string]any{
				"type":        "string",
				"description": "Entity id for get_state or call_service, for example light.kitchen",
			},
			"domain": map[string]any{
				"type":        "string",
				"description": "Entity domain filter for list_entities or service domain for call_service",
			},
			"service": map[string]any{
				"type":        "string",
				"description": "Service name for call_service, for example turn_on",
			},
			"entity_prefix": map[string]any{
				"type":        "string",
				"description": "Optional entity id prefix filter for list_entities",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of entities to return",
			},
			"data": map[string]any{
				"type":        "object",
				"description": "Additional Home Assistant service data to send with call_service",
			},
		},
		"required": []string{"action"},
	}
}

func (t *HomeAssistantTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action, _ := args["action"].(string)
	action = strings.TrimSpace(action)
	if action == "" {
		return ErrorResult("action is required")
	}

	var (
		output string
		err    error
	)
	switch action {
	case "status":
		output, err = t.status(ctx)
	case "list_entities":
		output, err = t.listEntities(ctx, args)
	case "get_state":
		output, err = t.getState(ctx, args)
	case "call_service":
		output, err = t.callService(ctx, args)
	default:
		return ErrorResult("unknown home_assistant action: " + action)
	}
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}
	return UserResult(output)
}

func (t *HomeAssistantTool) status(ctx context.Context) (string, error) {
	var payload struct {
		Message string `json:"message"`
	}
	if err := t.doJSON(ctx, http.MethodGet, "/api/", nil, &payload); err != nil {
		return "", err
	}
	msg := strings.TrimSpace(payload.Message)
	if msg == "" {
		msg = "API reachable."
	}
	return "Home Assistant status: " + msg, nil
}

func (t *HomeAssistantTool) listEntities(ctx context.Context, args map[string]any) (string, error) {
	limit, err := getInt64Arg(args, "limit", defaultHAEntityLimit)
	if err != nil {
		return "", err
	}
	if limit <= 0 {
		limit = defaultHAEntityLimit
	}
	if limit > maxHAEntityLimit {
		limit = maxHAEntityLimit
	}

	domain, _ := args["domain"].(string)
	prefix, _ := args["entity_prefix"].(string)
	domain = strings.ToLower(strings.TrimSpace(domain))
	prefix = strings.ToLower(strings.TrimSpace(prefix))

	var states []homeAssistantState
	if err := t.doJSON(ctx, http.MethodGet, "/api/states", nil, &states); err != nil {
		return "", err
	}

	filtered := make([]homeAssistantState, 0, len(states))
	for _, state := range states {
		entityID := strings.ToLower(strings.TrimSpace(state.EntityID))
		if entityID == "" {
			continue
		}
		if domain != "" && !strings.HasPrefix(entityID, domain+".") {
			continue
		}
		if prefix != "" && !strings.HasPrefix(entityID, prefix) {
			continue
		}
		filtered = append(filtered, state)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].EntityID < filtered[j].EntityID
	})

	lines := []string{fmt.Sprintf("Home Assistant entities: %d matched", len(filtered))}
	for i, state := range filtered {
		if int64(i) >= limit {
			break
		}
		name := friendlyName(state.Attributes)
		if name != "" && name != state.EntityID {
			lines = append(lines, fmt.Sprintf("- %s = %s (%s)", state.EntityID, state.State, name))
		} else {
			lines = append(lines, fmt.Sprintf("- %s = %s", state.EntityID, state.State))
		}
	}
	if len(filtered) == 0 {
		lines = append(lines, "No entities matched the requested filters.")
	}
	return strings.Join(lines, "\n"), nil
}

func (t *HomeAssistantTool) getState(ctx context.Context, args map[string]any) (string, error) {
	entityID, _ := args["entity_id"].(string)
	entityID = strings.TrimSpace(entityID)
	if entityID == "" {
		return "", fmt.Errorf("entity_id is required for get_state")
	}

	var state homeAssistantState
	if err := t.doJSON(ctx, http.MethodGet, "/api/states/"+url.PathEscape(entityID), nil, &state); err != nil {
		return "", err
	}

	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (t *HomeAssistantTool) callService(ctx context.Context, args map[string]any) (string, error) {
	domain, _ := args["domain"].(string)
	service, _ := args["service"].(string)
	entityID, _ := args["entity_id"].(string)
	domain = strings.TrimSpace(domain)
	service = strings.TrimSpace(service)
	entityID = strings.TrimSpace(entityID)
	if domain == "" || service == "" {
		return "", fmt.Errorf("domain and service are required for call_service")
	}

	payload := map[string]any{}
	if raw, ok := args["data"].(map[string]any); ok && raw != nil {
		for k, v := range raw {
			payload[k] = v
		}
	}
	if entityID != "" {
		payload["entity_id"] = entityID
	}

	var changed []homeAssistantState
	if err := t.doJSON(ctx, http.MethodPost, "/api/services/"+url.PathEscape(domain)+"/"+url.PathEscape(service), payload, &changed); err != nil {
		return "", err
	}

	lines := []string{
		fmt.Sprintf("Home Assistant service called: %s.%s", domain, service),
		fmt.Sprintf("Changed states returned: %d", len(changed)),
	}
	for i, state := range changed {
		if i >= 10 {
			break
		}
		lines = append(lines, fmt.Sprintf("- %s = %s", state.EntityID, state.State))
	}
	return strings.Join(lines, "\n"), nil
}

func (t *HomeAssistantTool) doJSON(ctx context.Context, method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", haToolUserAgent)

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(data))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("home assistant request failed: %s", msg)
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to decode Home Assistant response: %w", err)
	}
	return nil
}

func friendlyName(attrs map[string]any) string {
	if attrs == nil {
		return ""
	}
	raw, _ := attrs["friendly_name"].(string)
	return strings.TrimSpace(raw)
}
