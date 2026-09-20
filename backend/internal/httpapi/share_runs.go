package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func (a *ShareAPI) registerShareRuns(admin *gin.RouterGroup) {
	routes := admin.Group("/p115/share/generation-runs")
	routes.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if a.Runs == nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "后台生成服务尚未就绪"})
			return
		}
		c.Next()
	})
	respond := func(c *gin.Context, view service.ShareRunView, err error, code int) {
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, service.ErrGenerationInput) {
				status = 400
			}
			if errors.Is(err, service.ErrShareRunMissing) {
				status = 404
			}
			if errors.Is(err, service.ErrShareRunBusy) {
				status = 429
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(code, view)
	}
	routes.POST("", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
		var request struct {
			ID              string                  `json:"id"`
			CollectionScope string                  `json:"collectionScope"`
			Inputs          []service.GenerateInput `json:"inputs"`
		}
		if c.ShouldBindJSON(&request) != nil {
			c.JSON(400, gin.H{"error": "后台生成请求格式错误"})
			return
		}
		for i := range request.Inputs {
			if request.Inputs[i].BaseURL == "" {
				request.Inputs[i].BaseURL = a.generationBaseURL(c)
			}
		}
		if len(request.CollectionScope) > 160 {
			c.JSON(400, gin.H{"error": "收集范围无效"})
			return
		}
		ctx := service.WithShareCollectionScope(c.Request.Context(), request.CollectionScope)
		view, err := a.Runs.Submit(ctx, request.ID, request.Inputs)
		respond(c, view, err, 202)
	})
	routes.GET("/:id", func(c *gin.Context) { view, err := a.Runs.Get(c.Param("id")); respond(c, view, err, 200) })
	routes.POST("/:id/cancel", func(c *gin.Context) { view, err := a.Runs.Cancel(c.Param("id")); respond(c, view, err, 200) })
}
