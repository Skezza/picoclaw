package routing

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// defaultThreshold is used when the config threshold is zero or negative.
// At 0.35 a message needs at least one strong signal (code block, long text,
// or an attachment) before the heavy model is chosen.
const defaultThreshold = 0.35

// RouterConfig holds the validated model routing settings.
// It mirrors config.RoutingConfig but lives in pkg/routing to keep the
// dependency graph simple: pkg/agent resolves config → routing, not the reverse.
type RouterConfig struct {
	// LightModel is the model_name (from model_list) used for simple tasks.
	// Legacy field retained for backwards compatibility with the original
	// binary "light vs primary" router.
	LightModel string

	// Threshold is the complexity score cutoff in [0, 1].
	// score >= Threshold → primary (heavy) model.
	// score <  Threshold → light model.
	Threshold float64

	// Tiers are evaluated in order. The first tier whose MaxScore is greater
	// than or equal to the computed complexity score is selected.
	Tiers []config.RoutingTierConfig
}

// Router selects the appropriate model tier for each incoming message.
// It is safe for concurrent use from multiple goroutines.
type Router struct {
	cfg        RouterConfig
	classifier Classifier
}

const (
	DefaultFastTierName  = "fast"
	DefaultHeavyTierName = "heavy"
	DefaultToolsTierName = "tools"
	DefaultFreeTierName  = "free"
)

// New creates a Router with the given config and the default RuleClassifier.
// If cfg.Threshold is zero or negative, defaultThreshold (0.35) is used.
func New(cfg RouterConfig) *Router {
	if cfg.Threshold <= 0 {
		cfg.Threshold = defaultThreshold
	}
	return &Router{
		cfg:        cfg,
		classifier: &RuleClassifier{},
	}
}

// newWithClassifier creates a Router with a custom Classifier.
// Intended for unit tests that need to inject a deterministic scorer.
func newWithClassifier(cfg RouterConfig, c Classifier) *Router {
	if cfg.Threshold <= 0 {
		cfg.Threshold = defaultThreshold
	}
	return &Router{cfg: cfg, classifier: c}
}

// SelectModel returns the model to use for this conversation turn along with
// the computed complexity score (for logging and debugging).
//
//   - If score < cfg.Threshold: returns (cfg.LightModel, true, score)
//   - Otherwise:               returns (primaryModel, false, score)
//
// The caller is responsible for resolving the returned model name into
// provider candidates (see AgentInstance.LightCandidates).
func (r *Router) SelectModel(
	msg string,
	history []providers.Message,
	primaryModel string,
) (model string, usedLight bool, score float64) {
	tier, score := r.SelectTier(msg, history)
	if tier != "" {
		return r.cfg.LightModel, true, score
	}
	return primaryModel, false, score
}

// SelectTier returns the selected routing tier name, or the empty string when
// the primary agent model should be used. When a "tools" tier exists, prompts
// with early tool intent or recent tool activity are promoted there before
// score-based tier matching runs.
func (r *Router) SelectTier(msg string, history []providers.Message) (tier string, score float64) {
	features := ExtractFeatures(msg, history)
	score = r.classifier.Score(features)

	if len(r.cfg.Tiers) > 0 {
		if (features.ToolIntent || features.RecentToolCalls > 0) && r.hasAutoTier(DefaultToolsTierName) {
			return DefaultToolsTierName, score
		}
		for _, candidate := range r.cfg.Tiers {
			name := strings.TrimSpace(candidate.Name)
			if name == "" {
				continue
			}
			if candidate.MaxScore < 0 {
				continue
			}
			if strings.EqualFold(name, DefaultToolsTierName) {
				continue
			}
			if candidate.MaxScore == 0 || score <= candidate.MaxScore {
				return name, score
			}
		}
		return "", score
	}

	if features.ToolIntent || features.RecentToolCalls > 0 {
		return "", score
	}
	if score < r.cfg.Threshold {
		return "light", score
	}
	return "", score
}

// LightModel returns the configured light model name.
func (r *Router) LightModel() string {
	return r.cfg.LightModel
}

// Threshold returns the complexity threshold in use.
func (r *Router) Threshold() float64 {
	return r.cfg.Threshold
}

func (r *Router) hasAutoTier(name string) bool {
	for _, tier := range r.cfg.Tiers {
		if strings.EqualFold(strings.TrimSpace(tier.Name), name) && tier.MaxScore >= 0 {
			return true
		}
	}
	return false
}
