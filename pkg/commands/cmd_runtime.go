package commands

import (
	"context"
	"fmt"
	"strings"
)

func runtimeCommand() Definition {
	status := func(rt *Runtime) (string, error) {
		if rt == nil || rt.RuntimeStatus == nil {
			return "", fmt.Errorf("Runtime operations are not configured in this PicoClaw instance.")
		}
		return rt.RuntimeStatus()
	}

	capture := func(rt *Runtime, brief string) error {
		if rt == nil || rt.RuntimeCaptureBrief == nil {
			return fmt.Errorf("Runtime operations are not configured in this PicoClaw instance.")
		}
		return rt.RuntimeCaptureBrief(brief)
	}

	return Definition{
		Name:        "runtime",
		Description: "Work on host-local runtime config and services outside the repo worktree",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			intent := tailFromToken(req.Text, 1)
			if strings.TrimSpace(intent) != "" {
				if err := capture(rt, intent); err != nil {
					return req.Reply(err.Error())
				}
			}
			reply, err := status(rt)
			if err != nil {
				return req.Reply(err.Error())
			}
			if strings.TrimSpace(intent) == "" {
				return req.Reply(reply + "\nTalk normally to refine the runtime task, then run `/runtime execute` when you want me to start.")
			}
			return req.Reply(strings.Join([]string{
				reply,
				"Captured brief: " + summarizeCodexTask(intent),
				"Run `/runtime execute` when you want me to start on it.",
			}, "\n"))
		},
		SubCommands: []SubCommand{
			{
				Name:        "status",
				Description: "Show the current runtime task context for this host",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					reply, err := status(rt)
					if err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply(reply)
				},
			},
			{
				Name:        "execute",
				Description: "Launch a runtime task from the recent chat brief",
				ArgsUsage:   "[task override]",
				Handler: func(ctx context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.RuntimeExecute == nil {
						return req.Reply("Runtime operations are not configured in this PicoClaw instance.")
					}
					brief := tailFromToken(req.Text, 2)
					if strings.TrimSpace(brief) != "" && rt.RuntimeCaptureBrief != nil {
						if err := rt.RuntimeCaptureBrief(brief); err != nil {
							return req.Reply(err.Error())
						}
					}
					reply, err := rt.RuntimeExecute(ctx, brief)
					if err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply(reply)
				},
			},
		},
	}
}
