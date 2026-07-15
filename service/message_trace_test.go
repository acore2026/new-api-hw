package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func TestMessageTraceCaptureRedactsAndTruncates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer DisableMessageTrace()

	EnableMessageTrace(time.Minute, 64)
	storage, err := common.CreateBodyStorage([]byte(`{"model":"MiniMax-M2.7","Authorization":"Bearer sk-testsecret","messages":[{"role":"user","content":"hello world this should be truncated"}]}`))
	if err != nil {
		t.Fatalf("create body storage: %v", err)
	}
	defer storage.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	CaptureMessageTraceFromBodyStorage(ctx, storage)
	CaptureFinalMessageTrace(ctx, []byte(`{"api_key":"sk-finalsecret","messages":[{"role":"user","content":"hello"}]}`))
	responseBody := `data: {"access_token":"response-secret","choices":[]}` + "\n\n"
	response := &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": []string{"text/event-stream"}},
		ContentLength: int64(len(responseBody)),
		Body:          io.NopCloser(strings.NewReader(responseBody)),
	}
	WrapUpstreamResponseTrace(ctx, response)
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("read traced response: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close traced response: %v", err)
	}

	adminInfo := map[string]interface{}{}
	AttachMessageTrace(ctx, adminInfo)
	trace, ok := adminInfo["message_trace"].(map[string]interface{})
	if !ok {
		t.Fatalf("message_trace not attached: %#v", adminInfo)
	}

	body, _ := trace["body"].(string)
	if strings.Contains(body, "sk-testsecret") {
		t.Fatalf("trace body leaked token: %s", body)
	}
	if !strings.Contains(body, "<redacted>") {
		t.Fatalf("trace body did not contain redaction marker: %s", body)
	}
	if truncated, _ := trace["truncated"].(bool); !truncated {
		t.Fatalf("expected truncated trace, got %#v", trace)
	}
	if got, _ := trace["body_bytes"].(int64); got != storage.Size() {
		t.Fatalf("body_bytes = %d, want %d", got, storage.Size())
	}
	finalBody, _ := trace["final_body"].(string)
	if strings.Contains(finalBody, "sk-finalsecret") {
		t.Fatalf("final trace body leaked token: %s", finalBody)
	}
	if !strings.Contains(finalBody, "<redacted>") {
		t.Fatalf("final trace body did not contain redaction marker: %s", finalBody)
	}
	if got, _ := trace["final_body_bytes"].(int); got == 0 {
		t.Fatalf("final_body_bytes not recorded: %#v", trace)
	}
	if got, _ := trace["upstream_status"].(int); got != http.StatusOK {
		t.Fatalf("upstream_status = %d, want %d", got, http.StatusOK)
	}
	if got, _ := trace["upstream_content_type"].(string); got != "text/event-stream" {
		t.Fatalf("upstream_content_type = %q", got)
	}
	upstreamBody, _ := trace["upstream_body"].(string)
	if strings.Contains(upstreamBody, "response-secret") {
		t.Fatalf("upstream trace body leaked token: %s", upstreamBody)
	}
	if !strings.Contains(upstreamBody, "<redacted>") {
		t.Fatalf("upstream trace body did not contain redaction marker: %s", upstreamBody)
	}
	if got, _ := trace["upstream_body_bytes"].(int64); got != int64(len(responseBody)) {
		t.Fatalf("upstream_body_bytes = %d, want %d", got, len(responseBody))
	}
	if complete, _ := trace["upstream_complete"].(bool); !complete {
		t.Fatalf("expected complete upstream capture: %#v", trace)
	}
}

func TestMessageTraceDisabledDoesNotAttach(t *testing.T) {
	gin.SetMode(gin.TestMode)
	DisableMessageTrace()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	CaptureMessageTrace(ctx, []byte(`{"messages":[]}`))

	adminInfo := map[string]interface{}{}
	AttachMessageTrace(ctx, adminInfo)
	if _, ok := adminInfo["message_trace"]; ok {
		t.Fatalf("message_trace attached while disabled: %#v", adminInfo)
	}
}
