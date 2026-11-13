package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

type shareLinkTransferer interface {
	Transfer(context.Context, service.ShareTransferTarget, string, string) (service.ShareLinkTransferReceipt, error)
}

// 网页与 Bot 共用接收目录和转存服务，网页不接触账号凭据。
func RegisterShareLinkTransfer(admin *gin.RouterGroup, settings *service.ShareTransferSettings, transfer shareLinkTransferer) {
	group := admin.Group("/p115/share-transfer")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	})
	group.GET("/targets", func(c *gin.Context) {
		accountID := ""
		if settings.PrimaryID != nil {
			accountID = settings.PrimaryID()
		}
		if settings.ValidateAccount == nil || settings.ValidateAccount(accountID) != nil {
			accountID = ""
		}
		profiles := make([]gin.H, 0)
		for _, target := range settings.Targets() {
			if target.AccountID == accountID && settings.TargetReady(target) {
				profiles = append(profiles, shareTransferTargetView(target))
			}
		}
		c.JSON(http.StatusOK, gin.H{"profiles": profiles, "accountId": accountID})
	})
	group.POST("", func(c *gin.Context) {
		var input struct {
			Target   service.ShareTransferTarget `json:"target"`
			URL      string                      `json:"url"`
			Password string                      `json:"password"`
		}
		if err := c.ShouldBindJSON(&input); err != nil || input.URL == "" || len(input.URL) > 4096 || input.Target.AssignedBotID != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "115 分享转存参数无效"})
			return
		}
		current, ok := settings.Target(input.Target.ID)
		if !ok {
			shareTransferTargetError(c, service.ErrShareTransferTargetMissing)
			return
		}
		// Bot 分配与网页转存无关，目录与账号仍必须和点击时的快照一致。
		input.Target.AssignedBotID = current.AssignedBotID
		if err := settings.ValidateSnapshot(input.Target); err != nil {
			shareTransferTargetError(c, err)
			return
		}
		receipt, err := transfer.Transfer(c.Request.Context(), input.Target, input.URL, input.Password)
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, service.ErrGenerationInput) || errors.Is(err, service.ErrAccountInput) {
				status = http.StatusBadRequest
			} else if errors.Is(err, service.ErrShareTransferTargetChanged) || errors.Is(err, service.ErrShareTransferTargetMissing) || errors.Is(err, service.ErrAccountMissing) {
				status = http.StatusConflict
			}
			c.JSON(status, gin.H{"error": err.Error(), "receipt": receipt, "outcomeUncertain": receipt.AcceptedItems > 0 || errors.Is(err, service.ErrShareTransferUncertain)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"receipt": receipt})
	})
	group.GET("/profiles", func(c *gin.Context) {
		profiles := settings.Targets()
		views := make([]gin.H, 0, len(profiles))
		for _, profile := range profiles {
			views = append(views, shareTransferTargetView(profile))
		}
		c.JSON(http.StatusOK, gin.H{"profiles": views})
	})
	group.POST("/profiles", func(c *gin.Context) {
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
	group.PUT("/profiles/:profileID", func(c *gin.Context) {
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
	group.DELETE("/profiles/:profileID", func(c *gin.Context) {
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
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
