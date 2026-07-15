package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func TestRetryTraceAttachesToNormalAndMessageTrace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		DisableMessageTrace()
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	ctx.Set(retryTraceContextKey, &retryTraceState{
		configuredRetries: 12,
		configuredDelayMs: 250,
	})
	EnableMessageTrace(time.Minute, 1024)
	CaptureMessageTrace(ctx, []byte(`{"messages":[]}`))

	RecordRetryTrace(ctx, RetryTraceEntry{
		ChannelID:   7,
		ChannelName: "w3-a",
		StatusCode:  429,
		ErrorCode:   "InferHub.002002010.429",
		Error:       "quota exceeded",
		WillRetry:   true,
		DelayMs:     250,
	})
	RecordRetryTrace(ctx, RetryTraceEntry{
		ChannelID:   9,
		ChannelName: "w3-b",
		StatusCode:  503,
		ErrorCode:   "bad_response_status_code",
		Error:       "upstream unavailable",
		WillRetry:   false,
		DelayMs:     250,
	})

	adminInfo := map[string]interface{}{}
	AttachRetryTrace(ctx, adminInfo)
	AttachMessageTrace(ctx, adminInfo)

	snapshot, ok := adminInfo["retry_trace"].(*RetryTraceSnapshot)
	if !ok {
		t.Fatalf("retry_trace not attached to normal trace: %#v", adminInfo)
	}
	if snapshot.ConfiguredRetries != 12 || snapshot.ConfiguredDelayMs != 250 {
		t.Fatalf("unexpected retry configuration: %#v", snapshot)
	}
	if snapshot.RetryCount != 1 || snapshot.TotalDelayMs != 250 {
		t.Fatalf("unexpected retry totals: %#v", snapshot)
	}
	if len(snapshot.Entries) != 2 || snapshot.Entries[0].Attempt != 1 || snapshot.Entries[1].Attempt != 2 {
		t.Fatalf("unexpected retry entries: %#v", snapshot.Entries)
	}
	if snapshot.Entries[1].DelayMs != 0 {
		t.Fatalf("terminal failure retained a retry delay: %#v", snapshot.Entries[1])
	}

	messageTrace, ok := adminInfo["message_trace"].(map[string]interface{})
	if !ok {
		t.Fatalf("message_trace not attached: %#v", adminInfo)
	}
	if messageTrace["retry_trace"] == nil {
		t.Fatalf("retry_trace not nested in message_trace: %#v", messageTrace)
	}
}

func TestWaitRetryDelayStopsWhenRequestIsCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(requestContext)

	started := time.Now()
	if WaitRetryDelay(ctx, 1000) {
		t.Fatal("expected canceled request to stop retry delay")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("canceled retry delay took too long: %s", elapsed)
	}
}

func TestNormalizeRetryDelayMillisecondsClampsValue(t *testing.T) {
	if got := normalizeRetryDelayMilliseconds(-1); got != 0 {
		t.Fatalf("negative delay = %d, want 0", got)
	}
	if got := normalizeRetryDelayMilliseconds(common.MaxRetryDelayMilliseconds + 1); got != common.MaxRetryDelayMilliseconds {
		t.Fatalf("oversized delay = %d, want %d", got, common.MaxRetryDelayMilliseconds)
	}
}
