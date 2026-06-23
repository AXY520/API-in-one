package router

import (
	"api-in-one/handler"
	"api-in-one/middleware"
	"api-in-one/relay"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Setup configures all routes.
func Setup(engine *relay.Engine, pool *relay.Pool, indexHTML []byte, webAssets fs.FS) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())

	relayHandler := handler.NewRelay(engine)
	modelsHandler := handler.NewModels(pool)
	adminHandler := handler.NewAdmin(pool)
	protocolHandler := handler.NewProtocol(engine)

	// Serve SPA index page
	serveSPA := func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	}

	// Serve static assets
	assetFS, err := fs.Sub(webAssets, "web/dist/assets")
	if err == nil {
		r.StaticFS("/assets", http.FS(assetFS))
	} else {
		r.GET("/assets/*filepath", func(c *gin.Context) {
			c.Status(http.StatusNotFound)
		})
	}

	// Admin console SPA
	r.GET("/", serveSPA)
	r.GET("/admin", serveSPA)

	// Public: model list (no auth required)
	r.GET("/v1/models", modelsHandler.ListModels)

	// API v1 - OpenAI format (requires a client access key)
	v1 := r.Group("/v1", middleware.APIAuth())
	{
		v1.POST("/chat/completions", relayHandler.ChatCompletions)
		v1.POST("/messages", protocolHandler.ClaudeMessages)
		v1.POST("/responses", protocolHandler.Responses)
	}

	// Gemini format inbound
	gemini := r.Group("/v1beta", middleware.APIAuth())
	{
		gemini.POST("/models/:model", protocolHandler.GeminiGenerate)
	}

	// Admin API - requires admin key only
	admin := r.Group("/admin", middleware.Auth(), middleware.AdminRequired())
	{
		admin.GET("/channels", adminHandler.ListChannels)
		admin.POST("/channels", adminHandler.CreateChannel)
		admin.PUT("/channels/by-name", adminHandler.UpdateChannel)
		admin.PUT("/channels/by-name/state", adminHandler.UpdateChannelState)
		admin.PUT("/channels/by-name/routing", adminHandler.UpdateChannelRouting)
		admin.GET("/channels/by-name/keys", adminHandler.GetChannelKeys)
		admin.PUT("/channels/by-name/keys", adminHandler.UpdateChannelKeys)
		admin.PUT("/channels/by-name/keys/:index/state", adminHandler.UpdateChannelKeyState)
		admin.PUT("/channels/by-name/models/state", adminHandler.UpdateChannelModelState)
		admin.POST("/channels/by-name/probe", adminHandler.ProbeChannelKeys)
		admin.DELETE("/channels/by-name", adminHandler.DeleteChannel)
		admin.POST("/channels/reload", adminHandler.ReloadConfig)
		admin.GET("/settings", adminHandler.GetSettings)
		admin.PUT("/access-keys", adminHandler.UpdateAccessKeys)
		admin.PUT("/model-system-prompts", adminHandler.UpdateModelSystemPrompts)
		admin.PUT("/key-failure-policy", adminHandler.UpdateKeyFailurePolicy)
		admin.PUT("/channel-model-failure-policy", adminHandler.UpdateChannelModelFailurePolicy)
		admin.POST("/models/fetch", adminHandler.FetchModels)
		admin.GET("/stats", adminHandler.GetStats)
		admin.GET("/logs", adminHandler.GetLogs)
		admin.GET("/logs/export", adminHandler.ExportLogs)
		admin.GET("/logs/:id", adminHandler.GetLog)
		admin.DELETE("/logs", adminHandler.ClearLogs)
	}

	// NoRoute — serve SPA for non-API routes to support browser refresh
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/admin") || strings.HasPrefix(path, "/api") || strings.HasPrefix(path, "/v1") || strings.HasPrefix(path, "/v1beta") {
			c.Status(http.StatusNotFound)
			return
		}
		// Check if it's an asset request
		if strings.HasPrefix(path, "/assets/") {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})

	return r
}
