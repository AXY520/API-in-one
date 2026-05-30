package middleware

import (
	"api-in-one/config"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Auth validates Bearer token against admin_key or access_keys.
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "missing Authorization header", "type": "auth_error"},
			})
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "invalid Authorization format, expected Bearer token", "type": "auth_error"},
			})
			return
		}

		// Admin key → full access
		if adminKey := config.GetAdminKey(); adminKey != "" && token == adminKey {
			c.Set("is_admin", true)
			c.Set("api_key", token)
			c.Set("api_key_masked", maskKey(token))
			c.Next()
			return
		}

		// Access keys → user access
		for _, key := range config.GetAccessKeys() {
			if key != "" && token == key {
				c.Set("is_admin", false)
				c.Set("api_key", token)
				c.Set("api_key_masked", maskKey(token))
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"message": "invalid API key", "type": "auth_error"},
		})
	}
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// AdminRequired requires admin privileges. Use after Auth().
func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		isAdmin, _ := c.Get("is_admin")
		if isAdmin != true {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"message": "admin access required", "type": "auth_error"},
			})
			return
		}
		c.Next()
	}
}
