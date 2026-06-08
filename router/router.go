package router

import (
	"api-in-one/handler"
	"api-in-one/middleware"
	"api-in-one/relay"
	"os"

	"github.com/gin-gonic/gin"
)

// Setup configures all routes.
func Setup(engine *relay.Engine, pool *relay.Pool, indexHTML []byte) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())

	relayHandler := handler.NewRelay(engine)
	modelsHandler := handler.NewModels(pool)
	adminHandler := handler.NewAdmin(pool)
	protocolHandler := handler.NewProtocol(engine)

	// Public: serve static admin page
	serveIndex := func(c *gin.Context) {
		if data, err := os.ReadFile("./web/index.html"); err == nil {
			c.Data(200, "text/html; charset=utf-8", data)
			return
		}
		c.Data(200, "text/html; charset=utf-8", indexHTML)
	}
	r.GET("/", serveIndex)
	r.GET("/admin", serveIndex)

	// API v1 - OpenAI format (requires a client access key)
	v1 := r.Group("/v1", middleware.APIAuth())
	{
		v1.POST("/chat/completions", relayHandler.ChatCompletions)
		v1.GET("/models", modelsHandler.ListModels)
		// Claude format inbound
		v1.POST("/messages", protocolHandler.ClaudeMessages)
		// OpenAI Responses API format
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
		admin.GET("/channels/by-name/keys", adminHandler.GetChannelKeys)
		admin.PUT("/channels/by-name/keys", adminHandler.UpdateChannelKeys)
		admin.PUT("/channels/by-name/keys/:index/state", adminHandler.UpdateChannelKeyState)
		admin.POST("/channels/by-name/probe", adminHandler.ProbeChannelKeys)
		admin.DELETE("/channels/by-name", adminHandler.DeleteChannel)
		admin.PUT("/channels/:name", adminHandler.UpdateChannel)
		admin.PUT("/channels/:name/state", adminHandler.UpdateChannelState)
		admin.GET("/channels/:name/keys", adminHandler.GetChannelKeys)
		admin.PUT("/channels/:name/keys", adminHandler.UpdateChannelKeys)
		admin.PUT("/channels/:name/keys/:index/state", adminHandler.UpdateChannelKeyState)
		admin.POST("/channels/:name/probe", adminHandler.ProbeChannelKeys)
		admin.DELETE("/channels/:name", adminHandler.DeleteChannel)
		admin.POST("/channels/reload", adminHandler.ReloadConfig)
		admin.GET("/settings", adminHandler.GetSettings)
		admin.PUT("/access-keys", adminHandler.UpdateAccessKeys)
		admin.PUT("/model-system-prompts", adminHandler.UpdateModelSystemPrompts)
		admin.PUT("/key-failure-policy", adminHandler.UpdateKeyFailurePolicy)
		admin.POST("/models/fetch", adminHandler.FetchModels)
		// Stats
		admin.GET("/stats", adminHandler.GetStats)
		admin.GET("/logs", adminHandler.GetLogs)
		admin.GET("/logs/export", adminHandler.ExportLogs)
		admin.GET("/logs/:id", adminHandler.GetLog)
		admin.DELETE("/logs", adminHandler.ClearLogs)
	}

	return r
}
