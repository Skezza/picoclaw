// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/mcp"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type mcpRuntime struct {
	initOnce sync.Once
	mu       sync.Mutex
	manager  *mcp.Manager
	initErr  error
}

func (r *mcpRuntime) setManager(manager *mcp.Manager) {
	r.mu.Lock()
	r.manager = manager
	r.initErr = nil
	r.mu.Unlock()
}

func (r *mcpRuntime) setInitErr(err error) {
	r.mu.Lock()
	r.initErr = err
	r.mu.Unlock()
}

func (r *mcpRuntime) getInitErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.initErr
}

func (r *mcpRuntime) takeManager() *mcp.Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	manager := r.manager
	r.manager = nil
	return manager
}

func (r *mcpRuntime) hasManager() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manager != nil
}

// ensureMCPInitialized loads MCP servers/tools once so both Run() and direct
// agent mode share the same initialization path.
func (al *AgentLoop) ensureMCPInitialized(ctx context.Context) error {
	mcpCfg := effectiveMCPConfig(al.cfg)
	if !mcpCfg.Enabled {
		return nil
	}

	if mcpCfg.Servers == nil || len(mcpCfg.Servers) == 0 {
		logger.WarnCF("agent", "MCP is enabled but no servers are configured, skipping MCP initialization", nil)
		return nil
	}

	findValidServer := false
	for _, serverCfg := range mcpCfg.Servers {
		if serverCfg.Enabled {
			findValidServer = true
		}
	}
	if !findValidServer {
		logger.WarnCF("agent", "MCP is enabled but no valid servers are configured, skipping MCP initialization", nil)
		return nil
	}

	al.mcp.initOnce.Do(func() {
		mcpManager := mcp.NewManager()

		defaultAgent := al.registry.GetDefaultAgent()
		workspacePath := al.cfg.WorkspacePath()
		if defaultAgent != nil && defaultAgent.Workspace != "" {
			workspacePath = defaultAgent.Workspace
		}

		if err := mcpManager.LoadFromMCPConfig(ctx, mcpCfg, workspacePath); err != nil {
			logger.WarnCF("agent", "Failed to load MCP servers, MCP tools will not be available",
				map[string]any{
					"error": err.Error(),
				})
			if closeErr := mcpManager.Close(); closeErr != nil {
				logger.ErrorCF("agent", "Failed to close MCP manager",
					map[string]any{
						"error": closeErr.Error(),
					})
			}
			return
		}

		// Register MCP tools for all agents
		servers := mcpManager.GetServers()
		uniqueTools := 0
		totalRegistrations := 0
		agentIDs := al.registry.ListAgentIDs()
		agentCount := len(agentIDs)

		for serverName, conn := range servers {
			uniqueTools += len(conn.Tools)

			// Determine whether this server's tools should be deferred (hidden).
			// Per-server "deferred" field takes precedence over the global Discovery.Enabled.
			serverCfg := mcpCfg.Servers[serverName]
			registerAsHidden := serverIsDeferred(mcpCfg.Discovery.Enabled, serverCfg)

			for _, tool := range conn.Tools {
				for _, agentID := range agentIDs {
					agent, ok := al.registry.GetAgent(agentID)
					if !ok {
						continue
					}

					mcpTool := tools.NewMCPTool(mcpManager, serverName, tool)
					mcpTool.SetWorkspace(agent.Workspace)
					mcpTool.SetMaxInlineTextRunes(al.cfg.Tools.MCP.GetMaxInlineTextChars())

					if registerAsHidden {
						agent.Tools.RegisterHidden(mcpTool)
					} else {
						agent.Tools.Register(mcpTool)
					}

					totalRegistrations++
					logger.DebugCF("agent", "Registered MCP tool",
						map[string]any{
							"agent_id": agentID,
							"server":   serverName,
							"tool":     tool.Name,
							"name":     mcpTool.Name(),
							"deferred": registerAsHidden,
						})
				}
			}
		}
		logger.InfoCF("agent", "MCP tools registered successfully",
			map[string]any{
				"server_count":        len(servers),
				"unique_tools":        uniqueTools,
				"total_registrations": totalRegistrations,
				"agent_count":         agentCount,
			})

		// Initializes Discovery Tools only if enabled by configuration
		if mcpCfg.Enabled && mcpCfg.Discovery.Enabled {
			useBM25 := mcpCfg.Discovery.UseBM25
			useRegex := mcpCfg.Discovery.UseRegex

			// Fail fast: If discovery is enabled but no search method is turned on
			if !useBM25 && !useRegex {
				al.mcp.setInitErr(fmt.Errorf(
					"tool discovery is enabled but neither 'use_bm25' nor 'use_regex' is set to true in the configuration",
				))
				if closeErr := mcpManager.Close(); closeErr != nil {
					logger.ErrorCF("agent", "Failed to close MCP manager",
						map[string]any{
							"error": closeErr.Error(),
						})
				}
				return
			}

			ttl := mcpCfg.Discovery.TTL
			if ttl <= 0 {
				ttl = 5 // Default value
			}

			maxSearchResults := mcpCfg.Discovery.MaxSearchResults
			if maxSearchResults <= 0 {
				maxSearchResults = 5 // Default value
			}

			logger.InfoCF("agent", "Initializing tool discovery", map[string]any{
				"bm25": useBM25, "regex": useRegex, "ttl": ttl, "max_results": maxSearchResults,
			})

			for _, agentID := range agentIDs {
				agent, ok := al.registry.GetAgent(agentID)
				if !ok {
					continue
				}

				if useRegex {
					agent.Tools.Register(tools.NewRegexSearchTool(agent.Tools, ttl, maxSearchResults))
				}
				if useBM25 {
					agent.Tools.Register(tools.NewBM25SearchTool(agent.Tools, ttl, maxSearchResults))
				}
			}
		}

		al.mcp.setManager(mcpManager)
	})

	return al.mcp.getInitErr()
}

func effectiveMCPConfig(cfg *config.Config) config.MCPConfig {
	if cfg == nil {
		return config.MCPConfig{}
	}
	effective := cfg.Tools.MCP
	if effective.Servers == nil {
		effective.Servers = make(map[string]config.MCPServerConfig)
	}

	if !cfg.Tools.HomeAssistant.Enabled {
		return effective
	}
	if _, exists := effective.Servers["home_assistant"]; exists {
		return effective
	}

	command, err := resolveCompatMCPBinary("picoclaw-mcp-homeassistant")
	if err != nil {
		logger.WarnCF("agent", "Home Assistant MCP compatibility server unavailable",
			map[string]any{"error": err.Error()})
		return effective
	}

	falseValue := false
	env := map[string]string{
		"HOME_ASSISTANT_BASE_URL": strings.TrimSpace(cfg.Tools.HomeAssistant.BaseURL),
		"HOME_ASSISTANT_TOKEN":    cfg.Tools.HomeAssistant.Token.String(),
	}
	if cfg.Tools.HomeAssistant.TimeoutSeconds > 0 {
		env["HOME_ASSISTANT_TIMEOUT_SECONDS"] = strconv.Itoa(cfg.Tools.HomeAssistant.TimeoutSeconds)
	}

	effective.Enabled = true
	effective.Servers["home_assistant"] = config.MCPServerConfig{
		Enabled:  true,
		Deferred: &falseValue,
		Command:  command,
		Type:     "stdio",
		Env:      env,
	}
	return effective
}

func resolveCompatMCPBinary(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("binary name is required")
	}
	if path, err := exec.LookPath(name); err == nil && strings.TrimSpace(path) != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("%s binary not found in PATH", name)
	}
	candidate := filepath.Join(home, ".local", "bin", name)
	if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
		return candidate, nil
	}
	return "", fmt.Errorf("%s binary not found in PATH or ~/.local/bin", name)
}

// serverIsDeferred reports whether an MCP server's tools should be registered
// as hidden (deferred/discovery mode).
//
// The per-server Deferred field takes precedence over the global discoveryEnabled
// default. When Deferred is nil, discoveryEnabled is used as the fallback.
func serverIsDeferred(discoveryEnabled bool, serverCfg config.MCPServerConfig) bool {
	if !discoveryEnabled {
		return false
	}
	if serverCfg.Deferred != nil {
		return *serverCfg.Deferred
	}
	return true
}
