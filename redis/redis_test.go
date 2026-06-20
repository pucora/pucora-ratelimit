package redis

import (
	"testing"

	"github.com/pucora/lura/v2/config"
)

func TestConfigGetter_missing(t *testing.T) {
	_, err := ConfigGetter(config.ExtraConfig{})
	if err != ErrNoExtraCfg {
		t.Errorf("expected ErrNoExtraCfg, got %v", err)
	}
}

func TestConfigGetter_wrongType(t *testing.T) {
	e := config.ExtraConfig{
		Namespace: "not-a-map",
	}
	_, err := ConfigGetter(e)
	if err != ErrWrongExtraCfg {
		t.Errorf("expected ErrWrongExtraCfg, got %v", err)
	}
}

func TestConfigGetter_full(t *testing.T) {
	e := config.ExtraConfig{
		Namespace: map[string]interface{}{
			"addr":     "redis.example.com:6379",
			"password": "s3cr3t",
			"db":       float64(2),
			"max_rate": float64(100),
			"capacity": float64(200),
			"strategy": "header",
			"key":      "X-API-Key",
		},
	}

	cfg, err := ConfigGetter(e)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Addr != "redis.example.com:6379" {
		t.Errorf("unexpected Addr: %s", cfg.Addr)
	}
	if cfg.Password != "s3cr3t" {
		t.Errorf("unexpected Password: %s", cfg.Password)
	}
	if cfg.DB != 2 {
		t.Errorf("unexpected DB: %d", cfg.DB)
	}
	if cfg.MaxRate != 100 {
		t.Errorf("unexpected MaxRate: %f", cfg.MaxRate)
	}
	if cfg.Capacity != 200 {
		t.Errorf("unexpected Capacity: %d", cfg.Capacity)
	}
	if cfg.Strategy != "header" {
		t.Errorf("unexpected Strategy: %s", cfg.Strategy)
	}
	if cfg.Key != "X-API-Key" {
		t.Errorf("unexpected Key: %s", cfg.Key)
	}
}

func TestConfigGetter_intTypes(t *testing.T) {
	e := config.ExtraConfig{
		Namespace: map[string]interface{}{
			"db":       int(3),
			"max_rate": int64(50),
			"capacity": int(75),
		},
	}

	cfg, err := ConfigGetter(e)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DB != 3 {
		t.Errorf("unexpected DB: %d", cfg.DB)
	}
	if cfg.MaxRate != 50 {
		t.Errorf("unexpected MaxRate: %f", cfg.MaxRate)
	}
	if cfg.Capacity != 75 {
		t.Errorf("unexpected Capacity: %d", cfg.Capacity)
	}
}

func TestConfigGetter_defaults(t *testing.T) {
	// Empty map — fields should stay at zero values
	e := config.ExtraConfig{
		Namespace: map[string]interface{}{},
	}

	cfg, err := ConfigGetter(e)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Addr != "" {
		t.Errorf("expected empty Addr, got %q", cfg.Addr)
	}
	if cfg.MaxRate != 0 {
		t.Errorf("expected zero MaxRate, got %f", cfg.MaxRate)
	}
}
