package router

import (
	"api-in-one/handler"
	"api-in-one/middleware"
	"api-in-one/relay"

	"github.com/gin-gonic/gin"
)

// Setup configures all routes.
func Setup(engine *relay.Engine, pool *relay.Pool) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())

	relayHandler := handler.NewRelay(engine)
	modelsHandler := handler.NewModels(pool)
	adminHandler := handler.NewAdmin(pool)
	protocolHandler := handler.NewProtocol(engine)

	// Public: serve static admin page
	r.StaticFile("/", "./web/index.html")
	r.StaticFile("/admin", "./web/index.html")

	// API v1 - OpenAI format (requires any valid key)
	v1 := r.Group("/v1", middleware.Auth())
	{
		v1.POST("/chat/completions", relayHandler.ChatCompletions)
		v1.GET("/models", modelsHandler.ListModels)
		// Claude format inbound
		v1.POST("/messages", protocolHandler.ClaudeMessages)
		// OpenAI Responses API format
		v1.POST("/responses", protocolHandler.Responses)
	}

	// Gemini format inbound
	gemini := r.Group("/v1beta", middleware.Auth())
	{
		gemini.POST("/models/:model", protocolHandler.GeminiGenerate)
	}

	// Admin API - requires admin key only
	admin := r.Group("/admin", middleware.Auth(), middleware.AdminRequired())
	{
		admin.GET("/channels", adminHandler.ListChannels)
		admin.POST("/channels", adminHandler.CreateChannel)
		admin.PUT("/channels/:name", adminHandler.UpdateChannel)
		admin.GET("/channels/:name/keys", adminHandler.GetChannelKeys)
		admin.PUT("/channels/:name/keys", adminHandler.UpdateChannelKeys)
		admin.DELETE("/channels/:name", adminHandler.DeleteChannel)
		admin.POST("/channels/reload", adminHandler.ReloadConfig)
		admin.GET("/settings", adminHandler.GetSettings)
		admin.POST("/models/fetch", adminHandler.FetchModels)
		// Stats
		admin.GET("/stats", adminHandler.GetStats)
		admin.GET("/logs", adminHandler.GetLogs)
	}

	return r
}
