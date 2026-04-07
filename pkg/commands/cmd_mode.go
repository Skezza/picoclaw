package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

func boostCommand() Definition {
	return Definition{
		Name:        "boost",
		Description: "Use the stronger model for your next message",
		Usage:       "/boost",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			targets := sessionModeTargets(rt)
			if targets.Boost.Target == "" {
				return req.Reply("Boost unavailable: no stronger model is configured.")
			}
			if rt == nil || rt.ArmNextModelMode == nil {
				return req.Reply(unavailableMsg)
			}
			if err := rt.ArmNextModelMode(targets.Boost.Target); err != nil {
				return req.Reply(err.Error())
			}
			if rt.ClearSessionWorkMode != nil {
				if err := rt.ClearSessionWorkMode(); err != nil {
					return req.Reply(err.Error())
				}
			}
			return req.Reply(fmt.Sprintf("Boost armed. Next message will use %s.", targets.Boost.Label))
		},
	}
}

func freeCommand() Definition {
	return Definition{
		Name:        "free",
		Description: "Use the manual free tier for this session",
		Usage:       "/free",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			targets := sessionModeTargets(rt)
			if targets.Free.Target == "" {
				return req.Reply("Free mode unavailable: free tier is not configured.")
			}
			if rt == nil || rt.SetSessionModelMode == nil {
				return req.Reply(unavailableMsg)
			}
			if err := rt.SetSessionModelMode(targets.Free.Target); err != nil {
				return req.Reply(err.Error())
			}
			if rt.ClearSessionWorkMode != nil {
				if err := rt.ClearSessionWorkMode(); err != nil {
					return req.Reply(err.Error())
				}
			}
			return req.Reply(fmt.Sprintf("Session mode set to free (%s).", targets.Free.Label))
		},
	}
}

func codeCommand() Definition {
	return Definition{
		Name:        "code",
		Description: "Use the coding-friendly default model for this session",
		Usage:       "/code",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			targets := sessionModeTargets(rt)
			if targets.Code.Target == "" {
				return req.Reply("Code mode unavailable: no default model is configured.")
			}
			if rt == nil || rt.SetSessionModelMode == nil || rt.SetSessionWorkMode == nil {
				return req.Reply(unavailableMsg)
			}
			if err := rt.SetSessionModelMode(targets.Code.Target); err != nil {
				return req.Reply(err.Error())
			}
			if err := rt.SetSessionWorkMode("code"); err != nil {
				return req.Reply(err.Error())
			}
			return req.Reply(fmt.Sprintf("Session mode set to code (%s).", targets.Code.Label))
		},
	}
}

func defaultCommand() Definition {
	return Definition{
		Name:        "default",
		Description: "Return this session to the default model",
		Usage:       "/default",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.ClearSessionModelMode == nil {
				return req.Reply(unavailableMsg)
			}
			if err := rt.ClearSessionModelMode(); err != nil {
				return req.Reply(err.Error())
			}
			if rt.ClearSessionWorkMode != nil {
				if err := rt.ClearSessionWorkMode(); err != nil {
					return req.Reply(err.Error())
				}
			}
			return req.Reply("Session mode set to default.")
		},
	}
}

func statusCommand() Definition {
	return Definition{
		Name:        "status",
		Description: "Show the current session model mode",
		Usage:       "/status",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil {
				return req.Reply(unavailableMsg)
			}

			currentModel, provider := "", ""
			if rt.GetModelInfo != nil {
				currentModel, provider = rt.GetModelInfo()
			}

			targets := sessionModeTargets(rt)
			workMode := ""
			if rt.GetSessionWorkMode != nil {
				workMode = strings.TrimSpace(rt.GetSessionWorkMode())
			}
			persistent, pending := "", ""
			if rt.GetSessionModelMode != nil {
				persistent, pending = rt.GetSessionModelMode()
			}

			lines := make([]string, 0, 6)
			if currentModel != "" {
				if provider != "" {
					lines = append(lines, fmt.Sprintf("Current Model: %s (Provider: %s)", currentModel, provider))
				} else {
					lines = append(lines, fmt.Sprintf("Current Model: %s", currentModel))
				}
			}
			lines = append(lines, fmt.Sprintf("Session Mode: %s", sessionModeDescription(persistent, pending, workMode, targets)))
			if rt.CodexActive != nil {
				if codex, ok := rt.CodexActive(); ok && codex != nil {
					lines = append(lines, fmt.Sprintf("Codex Session: %s (%s)", codex.Slug, codex.ID))
					lines = append(lines, fmt.Sprintf("Codex Repo Path: %s", codex.RepoPath))
				}
			}
			if pending != "" {
				lines = append(lines, fmt.Sprintf("Pending Boost: %s", sessionModeLabel(pending, targets)))
			} else {
				lines = append(lines, "Pending Boost: none")
			}
			lines = append(lines, fmt.Sprintf("Default Model: %s", targets.Code.Label))
			if targets.Boost.Target != "" && targets.Boost.Label != targets.Code.Label {
				lines = append(lines, fmt.Sprintf("Boost Model: %s", targets.Boost.Label))
			}
			if targets.Free.Label != "" {
				lines = append(lines, fmt.Sprintf("Free Model: %s", targets.Free.Label))
			}
			return req.Reply(strings.Join(lines, "\n"))
		},
	}
}

type sessionModeTarget struct {
	Target string
	Label  string
}

type sessionTargets struct {
	Code  sessionModeTarget
	Boost sessionModeTarget
	Free  sessionModeTarget
}

func sessionModeTargets(rt *Runtime) sessionTargets {
	targets := sessionTargets{
		Code: sessionModeTarget{
			Target: "gpt-5.4-mini",
			Label:  "gpt-5.4-mini",
		},
		Boost: sessionModeTarget{
			Target: "gpt-5.4",
			Label:  "gpt-5.4",
		},
	}
	if rt == nil || rt.Config == nil {
		return targets
	}

	defaultModel := strings.TrimSpace(rt.Config.Agents.Defaults.ModelName)
	if defaultModel != "" {
		targets.Code = sessionModeTarget{Target: defaultModel, Label: defaultModel}
	}
	if hasModelConfig(rt.Config, "gpt-5.4") {
		targets.Boost = sessionModeTarget{Target: "gpt-5.4", Label: "gpt-5.4"}
	} else {
		targets.Boost = targets.Code
	}
	if routing := rt.Config.Agents.Defaults.Routing; routing != nil {
		if free := strings.TrimSpace(routing.LightModel); free != "" {
			targets.Free = sessionModeTarget{Target: free, Label: free}
		}
	}
	return targets
}

func hasModelConfig(cfg *config.Config, modelName string) bool {
	if cfg == nil {
		return false
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return false
	}
	for _, mc := range cfg.ModelList {
		if mc == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(mc.ModelName), modelName) {
			return true
		}
	}
	return false
}

func sessionModeDescription(persistent, pending, workMode string, targets sessionTargets) string {
	if pending != "" {
		return fmt.Sprintf("boost armed for next message (%s)", sessionModeLabel(pending, targets))
	}
	if workMode == "code" {
		return fmt.Sprintf("code (%s)", sessionModeLabel(persistent, targets))
	}
	if persistent == "" {
		return "default"
	}
	if targets.Free.Target != "" && strings.EqualFold(persistent, targets.Free.Target) {
		return fmt.Sprintf("free (%s)", targets.Free.Label)
	}
	return fmt.Sprintf("custom (%s)", sessionModeLabel(persistent, targets))
}

func sessionModeLabel(value string, targets sessionTargets) string {
	value = strings.TrimSpace(value)
	switch {
	case targets.Boost.Target != "" && strings.EqualFold(value, targets.Boost.Target):
		return targets.Boost.Label
	case targets.Code.Target != "" && strings.EqualFold(value, targets.Code.Target):
		return targets.Code.Label
	case targets.Free.Target != "" && strings.EqualFold(value, targets.Free.Target):
		return targets.Free.Label
	default:
		return value
	}
}
