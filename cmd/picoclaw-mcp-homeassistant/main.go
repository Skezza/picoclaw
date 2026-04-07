package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultHomeAssistantBaseURL = "http://127.0.0.1:8123"
	defaultHomeAssistantTimeout = 15 * time.Second
	defaultHAEntityLimit        = 25
	maxHAEntityLimit            = 100
	haToolUserAgent             = "picoclaw-mcp-homeassistant/1.0"
)

type client struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

type haState struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged string         `json:"last_changed"`
	LastUpdated string         `json:"last_updated"`
}

type listEntitiesInput struct {
	Domain       string `json:"domain,omitempty" jsonschema:"Optional entity domain filter, for example light"`
	EntityPrefix string `json:"entity_prefix,omitempty" jsonschema:"Optional entity id prefix filter"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum number of entities to return"`
}

type getStateInput struct {
	EntityID string `json:"entity_id" jsonschema:"Entity id to inspect, for example light.kitchen"`
}

type callServiceInput struct {
	Domain   string         `json:"domain" jsonschema:"Home Assistant service domain, for example light"`
	Service  string         `json:"service" jsonschema:"Service name, for example turn_on"`
	EntityID string         `json:"entity_id,omitempty" jsonschema:"Optional entity id target"`
	Data     map[string]any `json:"data,omitempty" jsonschema:"Additional service data payload"`
}

func main() {
	cli, err := newClientFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "picoclaw-homeassistant",
		Version: "v0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "status",
		Description: "Check Home Assistant API reachability and auth status.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		text, err := cli.status(ctx)
		return textResult(text), nil, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_entities",
		Description: "List Home Assistant entities, optionally filtered by domain or entity prefix.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input listEntitiesInput) (*mcp.CallToolResult, any, error) {
		text, err := cli.listEntities(ctx, input)
		return textResult(text), nil, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_state",
		Description: "Read the current state and key attributes for a Home Assistant entity.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input getStateInput) (*mcp.CallToolResult, any, error) {
		text, err := cli.getState(ctx, input)
		return textResult(text), nil, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "call_service",
		Description: "Call a Home Assistant service such as light.turn_on or switch.turn_off.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input callServiceInput) (*mcp.CallToolResult, any, error) {
		text, err := cli.callService(ctx, input)
		return textResult(text), nil, err
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

func newClientFromEnv() (*client, error) {
	baseURL := strings.TrimSpace(os.Getenv("HOME_ASSISTANT_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultHomeAssistantBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("valid HOME_ASSISTANT_BASE_URL is required")
	}

	token := strings.TrimSpace(os.Getenv("HOME_ASSISTANT_TOKEN"))
	if token == "" {
		return nil, fmt.Errorf("HOME_ASSISTANT_TOKEN is required")
	}

	timeout := defaultHomeAssistantTimeout
	if raw := strings.TrimSpace(os.Getenv("HOME_ASSISTANT_TIMEOUT_SECONDS")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid HOME_ASSISTANT_TIMEOUT_SECONDS: %w", err)
		}
		if seconds > 0 {
			timeout = time.Duration(seconds) * time.Second
		}
	}

	return &client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
	}, nil
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

func (c *client) status(ctx context.Context) (string, error) {
	var payload struct {
		Message string `json:"message"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/", nil, &payload); err != nil {
		return "", err
	}
	msg := strings.TrimSpace(payload.Message)
	if msg == "" {
		msg = "API reachable."
	}
	return "Home Assistant status: " + msg, nil
}

func (c *client) listEntities(ctx context.Context, input listEntitiesInput) (string, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = defaultHAEntityLimit
	}
	if limit > maxHAEntityLimit {
		limit = maxHAEntityLimit
	}

	domain := strings.ToLower(strings.TrimSpace(input.Domain))
	prefix := strings.ToLower(strings.TrimSpace(input.EntityPrefix))

	var states []haState
	if err := c.doJSON(ctx, http.MethodGet, "/api/states", nil, &states); err != nil {
		return "", err
	}

	filtered := make([]haState, 0, len(states))
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
		if i >= limit {
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

func (c *client) getState(ctx context.Context, input getStateInput) (string, error) {
	entityID := strings.TrimSpace(input.EntityID)
	if entityID == "" {
		return "", fmt.Errorf("entity_id is required")
	}

	var state haState
	if err := c.doJSON(ctx, http.MethodGet, "/api/states/"+url.PathEscape(entityID), nil, &state); err != nil {
		return "", err
	}

	lines := []string{
		fmt.Sprintf("Entity: %s", state.EntityID),
		fmt.Sprintf("State: %s", state.State),
	}
	if name := friendlyName(state.Attributes); name != "" {
		lines = append(lines, fmt.Sprintf("Name: %s", name))
	}
	if len(state.Attributes) > 0 {
		keys := make([]string, 0, len(state.Attributes))
		for key := range state.Attributes {
			if key == "friendly_name" {
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, fmt.Sprintf("Attr %s: %v", key, state.Attributes[key]))
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (c *client) callService(ctx context.Context, input callServiceInput) (string, error) {
	domain := strings.TrimSpace(input.Domain)
	service := strings.TrimSpace(input.Service)
	if domain == "" || service == "" {
		return "", fmt.Errorf("domain and service are required")
	}

	payload := make(map[string]any, len(input.Data)+1)
	for key, value := range input.Data {
		payload[key] = value
	}
	if entityID := strings.TrimSpace(input.EntityID); entityID != "" {
		payload["entity_id"] = entityID
	}

	var response []haState
	if err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/api/services/%s/%s", url.PathEscape(domain), url.PathEscape(service)), payload, &response); err != nil {
		return "", err
	}

	lines := []string{fmt.Sprintf("Home Assistant service called: %s.%s", domain, service)}
	if len(response) > 0 {
		lines = append(lines, fmt.Sprintf("Updated entities: %d", len(response)))
		for _, state := range response {
			lines = append(lines, fmt.Sprintf("- %s = %s", state.EntityID, state.State))
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (c *client) doJSON(ctx context.Context, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to encode Home Assistant request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("failed to create Home Assistant request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", haToolUserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call Home Assistant: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read Home Assistant response: %w", err)
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(data))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("home assistant API error (%s): %s", resp.Status, msg)
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("failed to decode Home Assistant response: %w", err)
	}
	return nil
}

func friendlyName(attrs map[string]any) string {
	if attrs == nil {
		return ""
	}
	raw, ok := attrs["friendly_name"]
	if !ok {
		return ""
	}
	value, _ := raw.(string)
	return strings.TrimSpace(value)
}
