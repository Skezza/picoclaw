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
			_, plannerModel, _ := codexPlannerState(rt)
			return req.Reply(strings.Replace(
				formatCodexActivatedMessage(info, plannerModel, codexExecutorModel(rt)),
				"Codex session is active in planning mode.",
				"Self-improve session is active in planning mode.",
				1,
			))
		},
		SubCommands: []SubCommand{
			{
				Name:        "plan",
				Description: "Activate self-improve planning mode",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if _, err := activate(rt); err != nil {
						return req.Reply(err.Error())
					}
					if rt.ClearCodexApprovalPending != nil {
						rt.ClearCodexApprovalPending()
					}
					if rt.SetSessionWorkMode == nil {
						return req.Reply(unavailableMsg)
					}
					if err := rt.SetSessionWorkMode("codex-plan"); err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply("Self-improve planning mode enabled. I will plan PicoClaw changes first, then ask you to reply `proceed` when the run is ready.")
				},
			},
			{
				Name:        "execute",
				Description: "Arm self-improve execution in the configured self repo",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if _, err := activate(rt); err != nil {
						return req.Reply(err.Error())
					}
					if rt.ClearCodexApprovalPending != nil {
						rt.ClearCodexApprovalPending()
					}
					if rt.SetSessionWorkMode == nil {
						return req.Reply(unavailableMsg)
					}
					if err := rt.SetSessionWorkMode("codex-plan"); err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply("Self-improve execution is armed. Keep planning in chat, then reply `proceed` when you want me to launch the approved PicoClaw run.")
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
				Name:        "ship",
				Description: "Publish the latest self-improve run and fast-forward a target deploy branch",
				ArgsUsage:   "<target>",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.SelfImproveShip == nil {
						return req.Reply("Self-improve shipping is not configured in this PicoClaw instance.")
					}
					target := strings.TrimSpace(nthToken(req.Text, 2))
					if target == "" {
						return req.Reply("Usage: /self-improve ship <target>")
					}
					result, err := rt.SelfImproveShip(target)
					if err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply(result)
				},
			},
			{
				Name:        "targets",
				Description: "List configured self-improve deployment targets",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.ListSelfImproveTargets == nil {
						return req.Reply("Self-improve is not configured in this PicoClaw instance.")
					}
					targets := rt.ListSelfImproveTargets()
					if len(targets) == 0 {
						return req.Reply("No self-improve deploy targets are configured.")
					}
					return req.Reply("Self-improve targets:\n- " + strings.Join(targets, "\n- "))
				},
			},
		},
	}
}
