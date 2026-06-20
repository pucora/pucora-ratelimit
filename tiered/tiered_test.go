package tiered

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/pucora/lura/v2/config"
	"github.com/pucora/lura/v2/logging"
)

// newTestRouter sets up a gin engine with the tiered middleware under test.
func newTestRouter(cfg Config) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware(cfg, logging.NoOp))
	r.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

// doRequest performs a single GET /test with the given tier header value.
func doRequest(r *gin.Engine, tier, headerKey string) int {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	if tier != "" {
		req.Header.Set(headerKey, tier)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// TestConfigGetter_missing ensures ErrNoExtraCfg is returned when the namespace key is absent.
func TestConfigGetter_missing(t *testing.T) {
	_, err := ConfigGetter(config.ExtraConfig{})
	if err != ErrNoExtraCfg {
		t.Errorf("expected ErrNoExtraCfg, got %v", err)
	}
}

// TestConfigGetter_wrongType ensures ErrWrongExtraCfg is returned for non-map values.
func TestConfigGetter_wrongType(t *testing.T) {
	_, err := ConfigGetter(config.ExtraConfig{Namespace: "invalid"})
	if err != ErrWrongExtraCfg {
		t.Errorf("expected ErrWrongExtraCfg, got %v", err)
	}
}

// TestConfigGetter_full verifies that all fields are correctly parsed.
func TestConfigGetter_full(t *testing.T) {
	e := config.ExtraConfig{
		Namespace: map[string]interface{}{
			"strategy": "literal",
			"key":      "X-Tier",
			"default": map[string]interface{}{
				"max_rate": float64(5),
				"capacity": float64(5),
			},
			"tiers": map[string]interface{}{
				"gold": map[string]interface{}{
					"max_rate": float64(100),
					"capacity": float64(100),
				},
				"silver": map[string]interface{}{
					"max_rate": float64(10),
					"capacity": float64(10),
				},
			},
		},
	}

	cfg, err := ConfigGetter(e)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Strategy != "literal" {
		t.Errorf("unexpected Strategy: %s", cfg.Strategy)
	}
	if cfg.Key != "X-Tier" {
		t.Errorf("unexpected Key: %s", cfg.Key)
	}
	if cfg.Default.MaxRate != 5 {
		t.Errorf("unexpected Default.MaxRate: %f", cfg.Default.MaxRate)
	}
	if cfg.Tiers["gold"].MaxRate != 100 {
		t.Errorf("unexpected gold MaxRate: %f", cfg.Tiers["gold"].MaxRate)
	}
	if cfg.Tiers["silver"].MaxRate != 10 {
		t.Errorf("unexpected silver MaxRate: %f", cfg.Tiers["silver"].MaxRate)
	}
}

// TestMiddleware_goldHigherRateThanSilver verifies that a gold tier with a very
// high capacity allows more consecutive requests than a silver tier with a lower
// capacity before being throttled.
func TestMiddleware_goldHigherRateThanSilver(t *testing.T) {
	const headerKey = "X-Tier"

	cfg := Config{
		Strategy: "literal",
		Key:      headerKey,
		Tiers: map[string]TierConfig{
			"gold":   {MaxRate: 1000, Capacity: 100},
			"silver": {MaxRate: 2, Capacity: 2},
		},
		Default: TierConfig{MaxRate: 1, Capacity: 1},
	}

	r := newTestRouter(cfg)

	// Gold tier: 100-token bucket — all 100 requests should succeed immediately.
	goldAllowed := 0
	for i := 0; i < 100; i++ {
		if doRequest(r, "gold", headerKey) == http.StatusOK {
			goldAllowed++
		}
	}

	// Silver tier: 2-token bucket — only 2 requests should succeed immediately.
	silverAllowed := 0
	for i := 0; i < 10; i++ {
		if doRequest(r, "silver", headerKey) == http.StatusOK {
			silverAllowed++
		}
	}

	if goldAllowed < 100 {
		t.Errorf("gold tier: expected 100 allowed requests, got %d", goldAllowed)
	}
	if silverAllowed > 2 {
		t.Errorf("silver tier: expected at most 2 allowed requests, got %d", silverAllowed)
	}
	if goldAllowed <= silverAllowed {
		t.Errorf("gold (%d) should allow more requests than silver (%d)", goldAllowed, silverAllowed)
	}
}

// TestMiddleware_noRateConfig verifies that a tier with MaxRate=0 passes every request through.
func TestMiddleware_noRateConfig(t *testing.T) {
	cfg := Config{
		Key:     "X-Tier",
		Default: TierConfig{MaxRate: 0, Capacity: 0},
	}

	r := newTestRouter(cfg)

	for i := 0; i < 20; i++ {
		if code := doRequest(r, "", "X-Tier"); code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, code)
		}
	}
}

// TestMiddleware_unknownTierUsesDefault verifies that an unlisted tier falls back to Default.
func TestMiddleware_unknownTierUsesDefault(t *testing.T) {
	const headerKey = "X-Tier"
	cfg := Config{
		Key: headerKey,
		Tiers: map[string]TierConfig{
			"gold": {MaxRate: 1000, Capacity: 1000},
		},
		// Default allows only 1 request at a time.
		Default: TierConfig{MaxRate: 1, Capacity: 1},
	}

	r := newTestRouter(cfg)

	// First request for unknown tier should succeed.
	if code := doRequest(r, "unknown", headerKey); code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", code)
	}

	// Second request for the same unknown tier should be throttled.
	if code := doRequest(r, "unknown", headerKey); code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", code)
	}
}
