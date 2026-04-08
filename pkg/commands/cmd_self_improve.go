package commands

import (
	"context"
	"fmt"
	"strings"
)

func selfImproveCommand() Definition {
	activate := func(rt *Runtime) (*CodexSessionInfo, error) {
		if rt == nil || rt.SelfImproveActivate == nil {
			return nil, fmt.Errorf("Self-improve is not configured in this PicoClaw instance.")
		}
		return rt.SelfImproveActivate()
	}

	return Definition{
		Name:        "self-improve",
		Description: "Work on PicoClaw itself using the configured self repo",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			info, err := activate(rt)
			if err != nil {
				return req.Reply(err.Error())
			}
			intent := tailFromToken(req.Text, 1)
			if strings.TrimSpace(intent) != "" && rt != nil && rt.SelfImproveCaptureBrief != nil {
				if err := rt.SelfImproveCaptureBrief(intent); err != nil {
					return req.Reply(err.Error())
				}
			}
			if rt == nil || rt.SelfImproveStatus == nil {
				return req.Reply("Self-improve is not configured in this PicoClaw instance.")
			}
			status, err := rt.SelfImproveStatus()
			if err != nil {
				return req.Reply(err.Error())
			}
			if strings.TrimSpace(intent) == "" {
				return req.Reply(status + "\nTalk normally to refine the task, then run `/self-improve execute` when you want me to start.")
			}
			lines := []string{
				status,
				"Captured brief: " + summarizeCodexTask(intent),
				"Run `/self-improve execute` when you want me to start on it.",
			}
			_ = info
			return req.Reply(strings.Join(lines, "\n"))
		},
		SubCommands: []SubCommand{
			{
				Name:        "execute",
				Description: "Launch a self-improve run from the recent chat brief",
				ArgsUsage:   "[task override]",
				Handler: func(ctx context.Context, req Request, rt *Runtime) error {
					if _, err := activate(rt); err != nil {
						return req.Reply(err.Error())
					}
					if rt == nil || rt.SelfImproveExecute == nil {
						return req.Reply(unavailableMsg)
					}
					brief := tailFromToken(req.Text, 2)
					if strings.TrimSpace(brief) != "" && rt.SelfImproveCaptureBrief != nil {
						if err := rt.SelfImproveCaptureBrief(brief); err != nil {
							return req.Reply(err.Error())
						}
					}
					effectiveBrief := strings.TrimSpace(brief)
					if effectiveBrief == "" {
						effectiveBrief = currentSelfImproveBrief(rt)
					}
					if looksLikeRuntimeTask(effectiveBrief) {
						return req.Reply("That sounds like host-local runtime work rather than a repo change. Use `/runtime` to capture the task, then `/runtime execute` to run it.")
					}
					reply, err := rt.SelfImproveExecute(ctx, brief)
					if err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply(reply)
				},
			},
			{
				Name:        "status",
				Description: "Show self-improve session and target state",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.SelfImproveStatus == nil {
						return req.Reply("Self-improve is not configured in this PicoClaw instance.")
					}
					status, err := rt.SelfImproveStatus()
					if err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply(status)
				},
			},
			{
				Name:        "deploy",
				Description: "Publish the latest self-improve run and fast-forward a target deploy branch",
				ArgsUsage:   "<target>",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.SelfImproveDeploy == nil {
						return req.Reply("Self-improve deployment is not configured in this PicoClaw instance.")
					}
					target := strings.TrimSpace(nthToken(req.Text, 2))
					if target == "" {
						return req.Reply("Usage: /self-improve deploy <target>")
					}
					result, err := rt.SelfImproveDeploy(target)
					if err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply(result)
				},
			},
		},
	}
}

func currentSelfImproveBrief(rt *Runtime) string {
	if rt == nil || rt.SelfImproveStatus == nil {
		return ""
	}
	status, err := rt.SelfImproveStatus()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "Captured brief: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Captured brief: "))
		}
	}
	return ""
}

func looksLikeRuntimeTask(brief string) bool {
	brief = strings.ToLower(strings.TrimSpace(brief))
	if brief == "" {
		return false
	}
	runtimeHints := []string{
		"config.json",
		".security",
		"token",
		"secret",
		"env file",
		"systemctl",
		"docker",
		"service ",
		"restart ",
		"enable ",
		"disable ",
		"install on this host",
		"write to /home/",
		"write to /mnt/",
		"host-local",
		"local runtime",
		"home assistant token",
	}
	for _, hint := range runtimeHints {
		if strings.Contains(brief, hint) {
			return true
		}
	}
	return false
}
