package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/service"
)

func organizeError(c *gin.Context, err error) {
	status := http.StatusBadGateway
	if errors.Is(err, service.ErrOrganizeInput) || errors.Is(err, service.ErrGenerationInput) {
		status = http.StatusBadRequest
	}
	if errors.Is(err, service.ErrOrganizeConflict) || errors.Is(err, service.ErrAccountMissing) {
		status = http.StatusConflict
	}
	if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
