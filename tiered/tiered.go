// Package tiered provides a gin middleware that enforces per-tier rate limits.
// Each incoming request carries a tier identifier (read from a configurable
// request header). The middleware looks up the matching TierConfig entry and
// enforces an independent in-memory token bucket for every (tier, client-key)
// pair.
package tiered

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pucora/lura/v2/config"
	"github.com/pucora/lura/v2/logging"
)

// Namespace is the ExtraConfig key used to retrieve the tiered rate-limit config.
const Namespace = "qos/ratelimit/tiered"

// TierConfig holds the rate-limit parameters for a single tier.
type TierConfig struct {
	// MaxRate is the maximum number of requests per second for this tier.
	MaxRate float64 `json:"max_rate"`
	// Capacity is the token-bucket burst capacity for this tier.
	Capacity uint64 `json:"capacity"`
}

// Config is the top-level configuration for the tiered rate limiter.
type Config struct {
	// Strategy controls how the tier name is extracted from the request.
	// Currently "literal" is supported: the value of the header named by Key
	// is used verbatim as the tier name.
	Strategy string `json:"strategy"`
	// Key is the request header whose value identifies the tier,
	// e.g. "X-Tier".
	Key string `json:"key"`
	// Tiers maps tier names to their individual rate-limit parameters.
	Tiers map[string]TierConfig `json:"tiers"`
	// Default is the rate-limit configuration applied when the tier extracted
	// from the request is not present in Tiers.
	Default TierConfig `json:"default"`
}

// ZeroCfg is the zero value for Config.
var ZeroCfg = Config{}

var (
	// ErrNoExtraCfg is returned when the ExtraConfig map has no entry for Namespace.
	ErrNoExtraCfg = fmt.Errorf("no extra config for namespace %s", Namespace)
	// ErrWrongExtraCfg is returned when the entry cannot be parsed as a map.
	ErrWrongExtraCfg = fmt.Errorf("wrong extra config for namespace %s", Namespace)
)

// ConfigGetter parses the ExtraConfig for the tiered rate-limit namespace.
func ConfigGetter(e config.ExtraConfig) (Config, error) {
	v, ok := e[Namespace]
	if !ok {
		return ZeroCfg, ErrNoExtraCfg
	}
	tmp, ok := v.(map[string]interface{})
	if !ok {
		return ZeroCfg, ErrWrongExtraCfg
	}

	cfg := Config{
		Tiers: make(map[string]TierConfig),
	}

	if v, ok := tmp["strategy"]; ok {
		cfg.Strategy = fmt.Sprintf("%v", v)
	}
	if v, ok := tmp["key"]; ok {
		cfg.Key = fmt.Sprintf("%v", v)
	}
	if v, ok := tmp["default"]; ok {
		if d, ok := v.(map[string]interface{}); ok {
			cfg.Default = parseTierConfig(d)
		}
	}
	if v, ok := tmp["tiers"]; ok {
		if tiers, ok := v.(map[string]interface{}); ok {
			for name, tv := range tiers {
				if td, ok := tv.(map[string]interface{}); ok {
					cfg.Tiers[name] = parseTierConfig(td)
				}
			}
		}
	}

	return cfg, nil
}

// parseTierConfig extracts MaxRate and Capacity from a raw map.
func parseTierConfig(m map[string]interface{}) TierConfig {
	tc := TierConfig{}
	if v, ok := m["max_rate"]; ok {
		switch val := v.(type) {
		case int64:
			tc.MaxRate = float64(val)
		case int:
			tc.MaxRate = float64(val)
		case float64:
			tc.MaxRate = val
		}
	}
	if v, ok := m["capacity"]; ok {
		switch val := v.(type) {
		case int64:
			tc.Capacity = uint64(val)
		case int:
			tc.Capacity = uint64(val)
		case float64:
			tc.Capacity = uint64(val)
		}
	}
	return tc
}

// tokenBucket is a simple thread-safe token bucket used for in-memory rate
// limiting within this package. It is intentionally independent from the root
// package's TokenBucket so that the tiered package has no circular import.
type tokenBucket struct {
	mu           sync.Mutex
	tokens       float64
	capacity     float64
	refillRate   float64 // tokens per nanosecond
	lastRefillAt time.Time
}

func newTokenBucket(maxRate float64, capacity uint64) *tokenBucket {
	cap := float64(capacity)
	if cap < 1 {
		cap = 1
	}
	rate := maxRate
	if rate < 1e-9 {
		rate = 1e-9
	}
	return &tokenBucket{
		tokens:       cap,
		capacity:     cap,
		refillRate:   rate / float64(time.Second),
		lastRefillAt: time.Now(),
	}
}

// Allow returns true if the request can proceed, consuming one token.
func (tb *tokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := float64(now.Sub(tb.lastRefillAt))
	tb.lastRefillAt = now

	// Refill tokens based on elapsed time.
	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}

	if tb.tokens < 1 {
		return false
	}
	tb.tokens--
	return true
}

// bucketKey uniquely identifies a bucket: "<tier>:<client-key>".
type bucketKey struct {
	tier      string
	clientKey string
}

// Middleware returns a gin.HandlerFunc that enforces tiered rate limiting.
//
// The tier is read from the header specified by cfg.Key (Strategy "literal").
// An independent token bucket is maintained per (tier, request-IP) pair.
// When a request's tier is not listed in cfg.Tiers, cfg.Default is used.
// Requests with a zero MaxRate in the resolved tier config are allowed through
// unconditionally.
func Middleware(cfg Config, logger logging.Logger) gin.HandlerFunc {
	// buckets maps bucketKey → *tokenBucket
	var buckets sync.Map

	logger.Debug(fmt.Sprintf("[Tiered] rate limit middleware enabled. key=%s tiers=%d",
		cfg.Key, len(cfg.Tiers)))

	return func(c *gin.Context) {
		tier := c.Request.Header.Get(cfg.Key)

		tierCfg, ok := cfg.Tiers[tier]
		if !ok {
			tierCfg = cfg.Default
			if tier != "" {
				logger.Debug(fmt.Sprintf("[Tiered] tier %q not found, using default", tier))
			}
		}

		// If no rate configured, pass through.
		if tierCfg.MaxRate <= 0 {
			c.Next()
			return
		}

		capacity := tierCfg.Capacity
		if capacity == 0 {
			if tierCfg.MaxRate < 1 {
				capacity = 1
			} else {
				capacity = uint64(tierCfg.MaxRate)
			}
		}

		// Derive a per-request key combining tier + client IP.
		clientKey := c.ClientIP()
		key := bucketKey{tier: tier, clientKey: clientKey}

		val, _ := buckets.LoadOrStore(key, newTokenBucket(tierCfg.MaxRate, capacity))
		tb := val.(*tokenBucket)

		if !tb.Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": fmt.Sprintf("rate limit exceeded for tier %q", tier),
			})
			return
		}

		c.Next()
	}
}
