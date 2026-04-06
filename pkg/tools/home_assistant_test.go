package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHomeAssistantTool_RequiresToken(t *testing.T) {
	if _, err := NewHomeAssistantTool("http://127.0.0.1:8123", "", 5); err == nil {
		t.Fatal("expected missing token error")
	}
}

func TestHomeAssistantTool_Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/" {
			t.Fatalf("path = %q, want /api/", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "API running."})
	}))
	defer server.Close()

	tool, err := NewHomeAssistantTool(server.URL, "test-token", 5)
	if err != nil {
		t.Fatalf("NewHomeAssistantTool() error = %v", err)
	}

	result := tool.Execute(context.Background(), map[string]any{"action": "status"})
	if result.IsError {
		t.Fatalf("Execute() unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "API running") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestHomeAssistantTool_ListEntities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/states" {
			t.Fatalf("path = %q, want /api/states", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"entity_id": "light.kitchen", "state": "on", "attributes": map[string]any{"friendly_name": "Kitchen Light"}},
			{"entity_id": "switch.desk", "state": "off", "attributes": map[string]any{"friendly_name": "Desk Plug"}},
		})
	}))
	defer server.Close()

	tool, err := NewHomeAssistantTool(server.URL, "test-token", 5)
	if err != nil {
		t.Fatalf("NewHomeAssistantTool() error = %v", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action": "list_entities",
		"domain": "light",
	})
	if result.IsError {
		t.Fatalf("Execute() unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "light.kitchen = on") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "switch.desk") {
		t.Fatalf("ForLLM should not include filtered switch: %q", result.ForLLM)
	}
}

func TestHomeAssistantTool_CallService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/services/light/turn_on" {
			t.Fatalf("path = %q, want /api/services/light/turn_on", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if payload["entity_id"] != "light.kitchen" {
			t.Fatalf("entity_id = %#v", payload["entity_id"])
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"entity_id": "light.kitchen", "state": "on", "attributes": map[string]any{"friendly_name": "Kitchen Light"}},
		})
	}))
	defer server.Close()

	tool, err := NewHomeAssistantTool(server.URL, "test-token", 5)
	if err != nil {
		t.Fatalf("NewHomeAssistantTool() error = %v", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action":    "call_service",
		"domain":    "light",
		"service":   "turn_on",
		"entity_id": "light.kitchen",
	})
	if result.IsError {
		t.Fatalf("Execute() unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Home Assistant service called: light.turn_on") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "light.kitchen = on") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}
