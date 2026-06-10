package controller

import (
	"io"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type messageTraceRequest struct {
	DurationSeconds int64 `json:"duration_seconds"`
	MaxBytes        int64 `json:"max_bytes"`
}

func GetMessageTraceStatus(c *gin.Context) {
	common.ApiSuccess(c, service.GetMessageTraceSnapshot())
}

func EnableMessageTrace(c *gin.Context) {
	req := messageTraceRequest{
		DurationSeconds: int64(time.Minute / time.Second),
	}
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, service.EnableMessageTrace(time.Duration(req.DurationSeconds)*time.Second, req.MaxBytes))
}

func DisableMessageTrace(c *gin.Context) {
	common.ApiSuccess(c, service.DisableMessageTrace())
}
