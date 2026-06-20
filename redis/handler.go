package redis

import (
	"github.com/gin-gonic/gin"
	"github.com/pucora/lura/v2/config"
	"github.com/pucora/lura/v2/logging"
	"github.com/pucora/lura/v2/proxy"
	router "github.com/pucora/lura/v2/router/gin"
)

// HandlerFactory wraps the given HandlerFactory with Redis-backed rate limiting.
// If no "qos/ratelimit/router/redis" config is found for an endpoint, the
// original handler is returned unchanged.
func HandlerFactory(logger logging.Logger, next router.HandlerFactory) router.HandlerFactory {
	return func(cfg *config.EndpointConfig, p proxy.Proxy) gin.HandlerFunc {
		redisCfg, err := ConfigGetter(cfg.ExtraConfig)
		if err != nil {
			return next(cfg, p)
		}
		mw := Middleware(redisCfg, logger)
		h := next(cfg, p)
		return func(c *gin.Context) {
			mw(c)
			if !c.IsAborted() {
				h(c)
			}
		}
	}
}
