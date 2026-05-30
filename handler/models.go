package handler

import (
	"api-in-one/model"
	"api-in-one/relay"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Models handles GET /v1/models
type Models struct {
	pool *relay.Pool
}

func NewModels(pool *relay.Pool) *Models {
	return &Models{pool: pool}
}

func (h *Models) ListModels(c *gin.Context) {
	models := h.pool.GetAvailableModels()
	if isAdmin, _ := c.Get("is_admin"); isAdmin != true {
		filtered := make([]model.ModelObject, 0, len(models))
		for _, item := range models {
			if requestCanUseModel(c, item.ID) {
				filtered = append(filtered, item)
			}
		}
		models = filtered
	}
	c.JSON(http.StatusOK, model.ModelListResponse{
		Object: "list",
		Data:   models,
	})
}
