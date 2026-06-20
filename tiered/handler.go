package tiered

import (
	"github.com/gin-gonic/gin"
	"github.com/pucora/lura/v2/config"
	"github.com/pucora/lura/v2/logging"
	"github.com/pucora/lura/v2/proxy"
	router "github.com/pucora/lura/v2/router/gin"
)

// HandlerFactory wraps the given HandlerFactory with tiered rate limiting.
// If no "qos/ratelimit/tiered" config is found for an endpoint, the
// original handler is returned unchanged.
func HandlerFactory(logger logging.Logger, next router.HandlerFactory) router.HandlerFactory {
	return func(cfg *config.EndpointConfig, p proxy.Proxy) gin.HandlerFunc {
		tieredCfg, err := ConfigGetter(cfg.ExtraConfig)
		if err != nil {
			return next(cfg, p)
		}
		mw := Middleware(tieredCfg, logger)
		h := next(cfg, p)
		return func(c *gin.Context) {
			mw(c)
			if !c.IsAborted() {
				h(c)
			}
		}
	}
}
