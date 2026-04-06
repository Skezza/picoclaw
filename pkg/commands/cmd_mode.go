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
		Description: "Use the heavy routed model for your next message",
		Usage:       "/boost",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			targets := sessionModeTargets(rt)
			if targets.Heavy.Target == "" {
				return req.Reply("Boost unavailable: heavy routing tier is not configured.")
			}
			if rt == nil || rt.ArmNextModelMode == nil {
				return req.Reply(unavailableMsg)
			}
			if err := rt.ArmNextModelMode(targets.Heavy.Target); err != nil {
				return req.Reply(err.Error())
			}
			if rt.ClearSessionWorkMode != nil {
				if err := rt.ClearSessionWorkMode(); err != nil {
					return req.Reply(err.Error())
				}
			}
			return req.Reply(fmt.Sprintf("Boost armed. Next message will use %s.", targets.Heavy.Label))
		},
	}
}

func paidCommand() Definition {
	return Definition{
		Name:        "paid",
		Description: "Legacy alias for the heavy routed model",
		Usage:       "/paid",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			targets := sessionModeTargets(rt)
			if targets.Heavy.Target == "" {
				return req.Reply("Paid mode unavailable: heavy routing tier is not configured.")
			}
			if rt == nil || rt.SetSessionModelMode == nil {
				return req.Reply(unavailableMsg)
			}
			if err := rt.SetSessionModelMode(targets.Heavy.Target); err != nil {
				return req.Reply(err.Error())
			}
			if rt.ClearSessionWorkMode != nil {
				if err := rt.ClearSessionWorkMode(); err != nil {
					return req.Reply(err.Error())
				}
			}
			return req.Reply(fmt.Sprintf("Legacy paid mode set to heavy (%s).", targets.Heavy.Label))
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
		Description: "Use the tools routing tier for this session",
		Usage:       "/code",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			targets := sessionModeTargets(rt)
			if targets.Tools.Target == "" {
				return req.Reply("Code mode unavailable: tools tier is not configured.")
			}
			if rt == nil || rt.SetSessionModelMode == nil || rt.SetSessionWorkMode == nil {
				return req.Reply(unavailableMsg)
			}
			if err := rt.SetSessionModelMode(targets.Tools.Target); err != nil {
				return req.Reply(err.Error())
			}
			if err := rt.SetSessionWorkMode("code"); err != nil {
				return req.Reply(err.Error())
			}
			return req.Reply(fmt.Sprintf("Session mode set to code (%s).", targets.Tools.Label))
		},
	}
}

func routeCommand() Definition {
	return Definition{
		Name:        "route",
		Description: "Use automatic tier routing for this session",
		Usage:       "/route",
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
			return req.Reply("Session mode set to route.")
		},
	}
}

func defaultCommand() Definition {
	return Definition{
		Name:        "default",
		Description: "Return this session to default routing",
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
			return req.Reply("Session mode set to route.")
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

			lines := make([]string, 0, 5)
			if currentModel != "" {
				if provider != "" {
					lines = append(lines, fmt.Sprintf("Current Model: %s (Provider: %s)", currentModel, provider))
				} else {
					lines = append(lines, fmt.Sprintf("Current Model: %s", currentModel))
				}
			}
			lines = append(lines, fmt.Sprintf("Session Mode: %s", sessionModeDescription(persistent, pending, workMode, targets)))
			if workMode != "" {
				lines = append(lines, fmt.Sprintf("Work Mode: %s", workMode))
			}
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
			if targets.Fast.Label != "" {
				lines = append(lines, fmt.Sprintf("Fast Model: %s", targets.Fast.Label))
			}
			if targets.Heavy.Label != "" {
				lines = append(lines, fmt.Sprintf("Heavy Model: %s", targets.Heavy.Label))
			}
			if targets.Tools.Label != "" {
				lines = append(lines, fmt.Sprintf("Tools Model: %s", targets.Tools.Label))
			}
			if targets.Free.Label != "" {
				lines = append(lines, fmt.Sprintf("Free Model: %s", targets.Free.Label))
			}
			return req.Reply(strings.Join(lines, "\n"))
		},
	}
}

type sessionModeTarget struct {
	Name   string
	Target string
	Label  string
}

type sessionTargets struct {
	Fast  sessionModeTarget
	Heavy sessionModeTarget
	Tools sessionModeTarget
	Free  sessionModeTarget
}

func sessionModeTargets(rt *Runtime) sessionTargets {
	if rt == nil || rt.Config == nil {
		return sessionTargets{}
	}

	targets := sessionTargets{
		Heavy: sessionModeTarget{
			Name:   "heavy",
			Target: strings.TrimSpace(rt.Config.Agents.Defaults.ModelName),
			Label:  strings.TrimSpace(rt.Config.Agents.Defaults.ModelName),
		},
	}

	if rc := rt.Config.Agents.Defaults.Routing; rc != nil {
		if tierName, tierLabel := preferredRoutingTier(rc, "fast"); tierName != "" && tierLabel != "" {
			targets.Fast = sessionModeTarget{Name: "fast", Target: "tier:" + tierName, Label: tierLabel}
		}
		if tierName, tierLabel := preferredRoutingTier(rc, "heavy", "paid"); tierName != "" && tierLabel != "" {
			targets.Heavy = sessionModeTarget{Name: "heavy", Target: "tier:" + tierName, Label: tierLabel}
		}
		if tierName, tierLabel := preferredRoutingTier(rc, "tools"); tierName != "" && tierLabel != "" {
			targets.Tools = sessionModeTarget{Name: "tools", Target: "tier:" + tierName, Label: tierLabel}
		} else {
			targets.Tools = targets.Heavy
			targets.Tools.Name = "tools"
		}
		if tierName, tierLabel := preferredRoutingTier(rc, "free"); tierName != "" && tierLabel != "" {
			targets.Free = sessionModeTarget{Name: "free", Target: "tier:" + tierName, Label: tierLabel}
		} else if free := strings.TrimSpace(rc.LightModel); free != "" {
			targets.Free = sessionModeTarget{Name: "free", Target: free, Label: free}
		}
	}
	return targets
}

func preferredRoutingTier(rc *config.RoutingConfig, modes ...string) (name, label string) {
	if rc == nil {
		return "", ""
	}

	for _, mode := range modes {
		target := ""
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "paid":
			target = strings.TrimSpace(rc.PaidTier)
			if target == "" {
				target = "paid"
			}
		case "free":
			target = strings.TrimSpace(rc.FreeTier)
			if target == "" {
				target = "free"
			}
		case "fast", "heavy", "tools":
			target = strings.TrimSpace(mode)
		default:
			continue
		}

		for _, tier := range rc.Tiers {
			if !strings.EqualFold(strings.TrimSpace(tier.Name), target) || tier.Model == nil {
				continue
			}
			primary := strings.TrimSpace(tier.Model.Primary)
			if primary == "" {
				return "", ""
			}
			return strings.TrimSpace(tier.Name), primary
		}
	}
	return "", ""
}

func sessionModeDescription(persistent, pending, workMode string, targets sessionTargets) string {
	if pending != "" {
		return fmt.Sprintf("boost armed for next message (%s)", sessionModeLabel(pending, targets))
	}
	if workMode != "" {
		if persistent != "" {
			return fmt.Sprintf("%s (%s)", workMode, sessionModeLabel(persistent, targets))
		}
		return workMode
	}
	if persistent == "" {
		return "route (default)"
	}
	if targets.Heavy.Target != "" && strings.EqualFold(persistent, targets.Heavy.Target) {
		return fmt.Sprintf("heavy (%s)", targets.Heavy.Label)
	}
	if targets.Tools.Target != "" && strings.EqualFold(persistent, targets.Tools.Target) {
		return fmt.Sprintf("tools (%s)", targets.Tools.Label)
	}
	if targets.Free.Target != "" && strings.EqualFold(persistent, targets.Free.Target) {
		return fmt.Sprintf("free (%s)", targets.Free.Label)
	}
	return fmt.Sprintf("custom (%s)", sessionModeLabel(persistent, targets))
}

func sessionModeLabel(value string, targets sessionTargets) string {
	value = strings.TrimSpace(value)
	switch {
	case targets.Heavy.Target != "" && strings.EqualFold(value, targets.Heavy.Target):
		return targets.Heavy.Label
	case targets.Tools.Target != "" && strings.EqualFold(value, targets.Tools.Target):
		return targets.Tools.Label
	case targets.Free.Target != "" && strings.EqualFold(value, targets.Free.Target):
		return targets.Free.Label
	case targets.Fast.Target != "" && strings.EqualFold(value, targets.Fast.Target):
		return targets.Fast.Label
	default:
		return value
	}
}
