package service

import (
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const retryTraceContextKey = "retry_trace"

type RetryTraceEntry struct {
	Attempt     int    `json:"attempt"`
	ChannelID   int    `json:"channel_id,omitempty"`
	ChannelName string `json:"channel_name,omitempty"`
	StatusCode  int    `json:"status_code,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	Error       string `json:"error,omitempty"`
	WillRetry   bool   `json:"will_retry"`
	DelayMs     int    `json:"delay_ms"`
}

type RetryTraceSnapshot struct {
	ConfiguredRetries int               `json:"configured_retries"`
	ConfiguredDelayMs int               `json:"configured_delay_ms"`
	RetryCount        int               `json:"retry_count"`
	TotalDelayMs      int               `json:"total_delay_ms"`
	Entries           []RetryTraceEntry `json:"entries"`
}

type retryTraceState struct {
	mu                sync.Mutex
	configuredRetries int
	configuredDelayMs int
	entries           []RetryTraceEntry
}

func GetRetryDelayMilliseconds() int {
	return normalizeRetryDelayMilliseconds(common.RetryDelayMilliseconds)
}

func normalizeRetryDelayMilliseconds(delay int) int {
	if delay < 0 {
		return 0
	}
	if delay > common.MaxRetryDelayMilliseconds {
		return common.MaxRetryDelayMilliseconds
	}
	return delay
}

func RecordRetryTrace(ctx *gin.Context, entry RetryTraceEntry) {
	if ctx == nil {
		return
	}
	state := getOrCreateRetryTraceState(ctx)
	state.mu.Lock()
	defer state.mu.Unlock()

	entry.Attempt = len(state.entries) + 1
	entry.Error = common.LocalLogPreview(common.MaskSensitiveInfo(entry.Error))
	if !entry.WillRetry {
		entry.DelayMs = 0
	}
	state.entries = append(state.entries, entry)
}

func HasRetryTrace(ctx *gin.Context) bool {
	state := getRetryTraceState(ctx)
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return len(state.entries) > 0
}

func GetRetryTraceSnapshot(ctx *gin.Context) *RetryTraceSnapshot {
	state := getRetryTraceState(ctx)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.entries) == 0 {
		return nil
	}

	entries := append([]RetryTraceEntry(nil), state.entries...)
	snapshot := &RetryTraceSnapshot{
		ConfiguredRetries: state.configuredRetries,
		ConfiguredDelayMs: state.configuredDelayMs,
		Entries:           entries,
	}
	for _, entry := range entries {
		if entry.WillRetry {
			snapshot.RetryCount++
			snapshot.TotalDelayMs += entry.DelayMs
		}
	}
	return snapshot
}

func AttachRetryTrace(ctx *gin.Context, adminInfo map[string]interface{}) {
	if adminInfo == nil {
		return
	}
	if snapshot := GetRetryTraceSnapshot(ctx); snapshot != nil {
		adminInfo["retry_trace"] = snapshot
	}
}

func WaitRetryDelay(ctx *gin.Context, delayMs int) bool {
	if delayMs <= 0 {
		return true
	}
	timer := time.NewTimer(time.Duration(delayMs) * time.Millisecond)
	defer timer.Stop()

	if ctx == nil || ctx.Request == nil {
		<-timer.C
		return true
	}
	select {
	case <-timer.C:
		return true
	case <-ctx.Request.Context().Done():
		return false
	}
}

func getOrCreateRetryTraceState(ctx *gin.Context) *retryTraceState {
	if state := getRetryTraceState(ctx); state != nil {
		return state
	}
	state := &retryTraceState{
		configuredRetries: common.RetryTimes,
		configuredDelayMs: GetRetryDelayMilliseconds(),
	}
	ctx.Set(retryTraceContextKey, state)
	return state
}

func getRetryTraceState(ctx *gin.Context) *retryTraceState {
	if ctx == nil {
		return nil
	}
	value, ok := ctx.Get(retryTraceContextKey)
	if !ok || value == nil {
		return nil
	}
	state, _ := value.(*retryTraceState)
	return state
}
