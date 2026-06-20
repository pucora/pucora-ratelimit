// Package redis provides a Redis-backed rate limiting middleware for gin.
// NOTE: This is a stub implementation. The configuration and interface are
// production-ready, but the actual Redis connection is replaced by an
// in-memory token-bucket fallback. Replace the in-memory logic with a real
// Redis client (e.g. github.com/redis/go-redis) to enable distributed rate
// limiting across multiple gateway instances.
package redis

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pucora/lura/v2/config"
	"github.com/pucora/lura/v2/logging"
	pucorarate "github.com/pucora/pucora-ratelimit/v3"
)

// Namespace is the key used to store and access the Redis rate limit config
// in the KrakenD/Pucora ExtraConfig map.
const Namespace = "qos/ratelimit/router/redis"

// Config holds the configuration for the Redis-backed rate limiter.
type Config struct {
	// Addr is the Redis server address, e.g. "localhost:6379".
	Addr string `json:"addr"`
	// Password is the Redis AUTH password (empty string means no auth).
	Password string `json:"password"`
	// DB is the Redis database index to select.
	DB int `json:"db"`
	// MaxRate is the number of allowed requests per second.
	MaxRate float64 `json:"max_rate"`
	// Capacity is the token-bucket burst capacity.
	Capacity uint64 `json:"capacity"`
	// Strategy controls how the rate-limit key is derived from the request.
	// Supported values: "ip", "header", "jwt".
	Strategy string `json:"strategy"`
	// Key is the header name to use when Strategy is "header".
	Key string `json:"key"`
}

// ZeroCfg is the zero value for the Config struct.
var ZeroCfg = Config{}

var (
	// ErrNoExtraCfg is returned when no extra config is found for the namespace.
	ErrNoExtraCfg = fmt.Errorf("no extra config for namespace %s", Namespace)
	// ErrWrongExtraCfg is returned when the extra config cannot be parsed.
	ErrWrongExtraCfg = fmt.Errorf("wrong extra config for namespace %s", Namespace)
)

// ConfigGetter parses the ExtraConfig for the Redis rate limit namespace.
// It returns ZeroCfg and an error if the config is missing or malformed.
func ConfigGetter(e config.ExtraConfig) (Config, error) {
	v, ok := e[Namespace]
	if !ok {
		return ZeroCfg, ErrNoExtraCfg
	}
	tmp, ok := v.(map[string]interface{})
	if !ok {
		return ZeroCfg, ErrWrongExtraCfg
	}

	cfg := Config{}

	if v, ok := tmp["addr"]; ok {
		cfg.Addr = fmt.Sprintf("%v", v)
	}
	if v, ok := tmp["password"]; ok {
		cfg.Password = fmt.Sprintf("%v", v)
	}
	if v, ok := tmp["db"]; ok {
		switch val := v.(type) {
		case int64:
			cfg.DB = int(val)
		case int:
			cfg.DB = val
		case float64:
			cfg.DB = int(val)
		}
	}
	if v, ok := tmp["max_rate"]; ok {
		switch val := v.(type) {
		case int64:
			cfg.MaxRate = float64(val)
		case int:
			cfg.MaxRate = float64(val)
		case float64:
			cfg.MaxRate = val
		}
	}
	if v, ok := tmp["capacity"]; ok {
		switch val := v.(type) {
		case int64:
			cfg.Capacity = uint64(val)
		case int:
			cfg.Capacity = uint64(val)
		case float64:
			cfg.Capacity = uint64(val)
		}
	}
	if v, ok := tmp["strategy"]; ok {
		cfg.Strategy = fmt.Sprintf("%v", v)
	}
	if v, ok := tmp["key"]; ok {
		cfg.Key = fmt.Sprintf("%v", v)
	}

	return cfg, nil
}

// Middleware returns a gin.HandlerFunc that enforces rate limiting based on
// the supplied Config.
//
// Stub note: this implementation uses an in-memory token bucket keyed by the
// client identifier. In production, replace the tokenBucketStore with a Redis
// INCR / EXPIRE command pair (or a sliding-window Lua script) so that the
// limit is enforced across all gateway replicas.
func Middleware(cfg Config, logger logging.Logger) gin.HandlerFunc {
	if cfg.MaxRate <= 0 {
		// No rate limit configured — pass every request.
		return func(c *gin.Context) { c.Next() }
	}

	capacity := cfg.Capacity
	if capacity == 0 {
		if cfg.MaxRate < 1 {
			capacity = 1
		} else {
			capacity = uint64(cfg.MaxRate)
		}
	}

	logger.Debug(fmt.Sprintf(
		"[Redis stub] rate limit enabled. addr=%s strategy=%s maxRate=%f capacity=%d",
		cfg.Addr, cfg.Strategy, cfg.MaxRate, capacity,
	))

	// In-memory store: key → *pucorarate.TokenBucket
	// A sync.Map is used so that bucket creation is safe under concurrent
	// requests without holding a global lock for every Allow() check.
	var store sync.Map

	keyFunc := buildKeyFunc(cfg)

	return func(c *gin.Context) {
		key := keyFunc(c)
		if key == "" {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": pucorarate.ErrLimited.Error()})
			return
		}

		// Load or create the token bucket for this key.
		val, _ := store.LoadOrStore(key, pucorarate.NewTokenBucket(cfg.MaxRate, capacity))
		tb := val.(*pucorarate.TokenBucket)

		if !tb.Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": pucorarate.ErrLimited.Error()})
			return
		}
		c.Next()
	}
}

// buildKeyFunc returns a function that derives the rate-limit key from a
// gin.Context, based on the configured Strategy.
func buildKeyFunc(cfg Config) func(*gin.Context) string {
	switch strings.ToLower(cfg.Strategy) {
	case "header":
		header := cfg.Key
		return func(c *gin.Context) string {
			return c.Request.Header.Get(header)
		}
	case "jwt":
		// Stub: use the raw Authorization header value as the key.
		// A real implementation would decode the JWT and extract a subject claim.
		return func(c *gin.Context) string {
			return c.Request.Header.Get("Authorization")
		}
	default: // "ip" and anything else
		return func(c *gin.Context) string {
			return c.ClientIP()
		}
	}
}

// contextKey is an unexported type used for context values (reserved for
// future use, e.g. passing a redis.Client via context).
type contextKey struct{}

// withContext is a helper that attaches a value to a context under the
// package-private key (kept for future Redis client injection).
func withContext(ctx context.Context, val interface{}) context.Context {
	return context.WithValue(ctx, contextKey{}, val)
}

// fromContext retrieves a value attached by withContext (future use).
func fromContext(ctx context.Context) interface{} {
	return ctx.Value(contextKey{})
}

// Ensure the helpers above are referenced so the compiler does not complain
// about unused unexported identifiers during stub phase.
var (
	_ = withContext
	_ = fromContext
	_ = time.Second // imported for future TTL-based eviction
)
