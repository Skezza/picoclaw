package telegram

import (
	"context"
	"math/rand"
	"regexp"
	"slices"
	"time"

	"github.com/mymmrac/telego"

	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/logger"
)

var commandRegistrationBackoff = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	60 * time.Second,
	5 * time.Minute,
	10 * time.Minute,
}

func commandRegistrationDelay(attempt int) time.Duration {
	if len(commandRegistrationBackoff) == 0 {
		return 0
	}
	base := commandRegistrationBackoff[min(attempt, len(commandRegistrationBackoff)-1)]
	// Full jitter in [0.5, 1.0) to avoid synchronized retries across instances.
	return time.Duration(float64(base) * (0.5 + rand.Float64()*0.5))
}

func commandRegistrationCleanupScopes() []telego.BotCommandScope {
	return []telego.BotCommandScope{
		&telego.BotCommandScopeAllPrivateChats{Type: telego.ScopeTypeAllPrivateChats},
		&telego.BotCommandScopeAllGroupChats{Type: telego.ScopeTypeAllGroupChats},
		&telego.BotCommandScopeAllChatAdministrators{Type: telego.ScopeTypeAllChatAdministrators},
	}
}

var telegramCommandNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func buildBotCommands(defs []commands.Definition) []telego.BotCommand {
	botCommands := make([]telego.BotCommand, 0, len(defs))
	for _, def := range defs {
		if def.Name == "" || def.Description == "" {
			continue
		}
		if !telegramCommandNamePattern.MatchString(def.Name) {
			logger.WarnCF("telegram", "Skipping invalid Telegram command definition", map[string]any{
				"command": def.Name,
			})
			continue
		}
		botCommands = append(botCommands, telego.BotCommand{
			Command:     def.Name,
			Description: def.Description,
		})
	}
	return botCommands
}

func syncTelegramCommands(
	ctx context.Context,
	defs []commands.Definition,
	getCurrent func(context.Context, *telego.GetMyCommandsParams) ([]telego.BotCommand, error),
	setCommands func(context.Context, *telego.SetMyCommandsParams) error,
	deleteCommands func(context.Context, *telego.DeleteMyCommandsParams) error,
) error {
	botCommands := buildBotCommands(defs)

	for _, scope := range commandRegistrationCleanupScopes() {
		if err := deleteCommands(ctx, &telego.DeleteMyCommandsParams{Scope: scope}); err != nil {
			logger.WarnCF("telegram", "Failed to clear stale Telegram command scope; continuing",
				map[string]any{"scope": scope.ScopeType(), "error": err.Error()})
		}
	}

	current, err := getCurrent(ctx, &telego.GetMyCommandsParams{})
	if err != nil {
		// If we can't read current commands, fall through to set them.
		logger.WarnCF("telegram", "Failed to get current commands, will set unconditionally",
			map[string]any{"error": err.Error()})
	} else if slices.Equal(current, botCommands) {
		logger.DebugCF("telegram", "Bot commands are up to date", nil)
		return nil
	}

	return setCommands(ctx, &telego.SetMyCommandsParams{
		Commands: botCommands,
	})
}

// RegisterCommands registers bot commands on Telegram platform.
func (c *TelegramChannel) RegisterCommands(ctx context.Context, defs []commands.Definition) error {
	return syncTelegramCommands(
		ctx,
		defs,
		c.bot.GetMyCommands,
		c.bot.SetMyCommands,
		c.bot.DeleteMyCommands,
	)
}

func (c *TelegramChannel) startCommandRegistration(ctx context.Context, defs []commands.Definition) {
	if len(defs) == 0 {
		return
	}

	register := c.registerFunc
	if register == nil {
		register = c.RegisterCommands
	}

	regCtx, cancel := context.WithCancel(ctx)
	c.commandRegCancel = cancel

	// Registration runs asynchronously so Telegram message intake is never blocked
	// by temporary upstream API failures. Retry stops on success or channel shutdown.
	go func() {
		attempt := 0
		timer := time.NewTimer(0)
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		defer timer.Stop()
		for {
			err := register(regCtx, defs)
			if err == nil {
				logger.InfoCF("telegram", "Telegram commands registered", map[string]any{
					"count": len(defs),
				})
				return
			}

			delay := commandRegistrationDelay(attempt)
			logger.WarnCF("telegram", "Telegram command registration failed; will retry", map[string]any{
				"error":       err.Error(),
				"retry_after": delay.String(),
			})
			attempt++

			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(delay)

			select {
			case <-regCtx.Done():
				return
			case <-timer.C:
			}
		}
	}()
}
