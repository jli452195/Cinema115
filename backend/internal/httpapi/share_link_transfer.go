package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func RegisterShareLinkTransfer(admin *gin.RouterGroup, settings *service.ShareTransferSettings) {
	group := admin.Group("/p115/share-transfer/profiles")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	})
	group.GET("", func(c *gin.Context) {
		profiles := settings.Targets()
		views := make([]gin.H, 0, len(profiles))
		for _, profile := range profiles {
			views = append(views, shareTransferTargetView(profile))
		}
		c.JSON(http.StatusOK, gin.H{"profiles": views})
	})
	group.POST("", func(c *gin.Context) {
		var input service.ShareTransferTarget
		if err := c.ShouldBindJSON(&input); err != nil || input.ID != "" || input.AssignedBotID != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "115 分享转存目标参数无效"})
			return
		}
		profile, err := settings.SaveTarget(input)
		if err != nil {
			shareTransferTargetError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"profile": shareTransferTargetView(profile)})
	})
	group.PUT("/:profileID", func(c *gin.Context) {
		var input service.ShareTransferTarget
		if err := c.ShouldBindJSON(&input); err != nil || input.AssignedBotID != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "115 分享转存目标参数无效"})
			return
		}
		input.ID = c.Param("profileID")
		profile, err := settings.SaveTarget(input)
		if err != nil {
			shareTransferTargetError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"profile": shareTransferTargetView(profile)})
	})
	group.DELETE("/:profileID", func(c *gin.Context) {
		if err := settings.DeleteTarget(c.Param("profileID")); err != nil {
			shareTransferTargetError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func shareTransferTargetView(profile service.ShareTransferTarget) gin.H {
	return gin.H{"id": profile.ID, "name": profile.Name, "accountId": profile.AccountID, "targetCID": profile.TargetCID, "targetPath": profile.TargetPath}
}

func shareTransferTargetError(c *gin.Context, err error) {
	status := http.StatusConflict
	if errors.Is(err, service.ErrAccountInput) || errors.Is(err, service.ErrGenerationInput) {
		status = http.StatusBadRequest
	} else if errors.Is(err, service.ErrAccountMissing) {
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
