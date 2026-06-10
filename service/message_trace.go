package service

import (
	"io"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const (
	messageTraceContextKey       = "message_trace"
	defaultMessageTraceMaxBytes  = int64(20000)
	maxMessageTraceDuration      = time.Minute
	maxMessageTraceCaptureBytes  = int64(200000)
	messageTraceDefaultOperation = "relay"
)

var (
	messageTraceUntil    atomic.Int64
	messageTraceMaxBytes atomic.Int64
	secretFieldPattern   = regexp.MustCompile(`(?i)("?(authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|password|secret|key)"?\s*:\s*)"[^"]*"`)
	bearerPattern        = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]+`)
	skTokenPattern       = regexp.MustCompile(`sk-[a-zA-Z0-9_-]{8,}`)
)

type MessageTraceSnapshot struct {
	Enabled          bool  `json:"enabled"`
	Until            int64 `json:"until"`
	RemainingSeconds int64 `json:"remaining_seconds"`
	MaxBytes         int64 `json:"max_bytes"`
}

type messageTraceFinalReader struct {
	reader io.Reader
	max    int64
	mu     sync.Mutex
	buf    []byte
	total  int64
}

func (r *messageTraceFinalReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.mu.Lock()
		r.total += int64(n)
		remaining := int(r.max) - len(r.buf)
		if remaining > 0 {
			if n < remaining {
				remaining = n
			}
			r.buf = append(r.buf, p[:remaining]...)
		}
		r.mu.Unlock()
	}
	return n, err
}

func EnableMessageTrace(duration time.Duration, maxBytes int64) MessageTraceSnapshot {
	if duration <= 0 || duration > maxMessageTraceDuration {
		duration = maxMessageTraceDuration
	}
	if maxBytes <= 0 {
		maxBytes = defaultMessageTraceMaxBytes
	}
	if maxBytes > maxMessageTraceCaptureBytes {
		maxBytes = maxMessageTraceCaptureBytes
	}
	until := time.Now().Add(duration).Unix()
	messageTraceUntil.Store(until)
	messageTraceMaxBytes.Store(maxBytes)
	return GetMessageTraceSnapshot()
}

func DisableMessageTrace() MessageTraceSnapshot {
	messageTraceUntil.Store(0)
	return GetMessageTraceSnapshot()
}

func GetMessageTraceSnapshot() MessageTraceSnapshot {
	now := time.Now().Unix()
	until := messageTraceUntil.Load()
	maxBytes := messageTraceMaxBytes.Load()
	if maxBytes <= 0 {
		maxBytes = defaultMessageTraceMaxBytes
	}
	remaining := until - now
	if remaining <= 0 {
		return MessageTraceSnapshot{
			Enabled:          false,
			Until:            until,
			RemainingSeconds: 0,
			MaxBytes:         maxBytes,
		}
	}
	return MessageTraceSnapshot{
		Enabled:          true,
		Until:            until,
		RemainingSeconds: remaining,
		MaxBytes:         maxBytes,
	}
}

func WrapFinalMessageTraceReader(ctx *gin.Context, reader io.Reader) (io.Reader, func()) {
	if ctx == nil || reader == nil {
		return reader, func() {}
	}
	snapshot := GetMessageTraceSnapshot()
	if !snapshot.Enabled {
		return reader, func() {}
	}
	traceReader := &messageTraceFinalReader{
		reader: reader,
		max:    snapshot.MaxBytes,
	}
	return traceReader, func() {
		traceReader.mu.Lock()
		raw := append([]byte(nil), traceReader.buf...)
		total := traceReader.total
		traceReader.mu.Unlock()
		body, truncated := sanitizeMessageTraceBody(raw, snapshot.MaxBytes)
		if total > int64(len(raw)) {
			truncated = true
		}
		attachMessageTraceFields(ctx, map[string]interface{}{
			"final_body":       body,
			"final_body_bytes": total,
			"final_truncated":  truncated,
		})
	}
}

func CaptureFinalMessageTrace(ctx *gin.Context, rawBody []byte) {
	if ctx == nil || rawBody == nil {
		return
	}
	snapshot := GetMessageTraceSnapshot()
	if !snapshot.Enabled {
		return
	}
	body, truncated := sanitizeMessageTraceBody(rawBody, snapshot.MaxBytes)
	attachMessageTraceFields(ctx, map[string]interface{}{
		"final_body":       body,
		"final_body_bytes": len(rawBody),
		"final_truncated":  truncated,
	})
}

func CaptureMessageTrace(ctx *gin.Context, rawBody []byte) {
	if ctx == nil || rawBody == nil {
		return
	}
	snapshot := GetMessageTraceSnapshot()
	if !snapshot.Enabled {
		return
	}
	body, truncated := sanitizeMessageTraceBody(rawBody, snapshot.MaxBytes)
	ctx.Set(messageTraceContextKey, map[string]interface{}{
		"captured_at":    common.GetTimestamp(),
		"expires_at":     snapshot.Until,
		"operation":      messageTraceDefaultOperation,
		"body":           body,
		"body_bytes":     len(rawBody),
		"max_bytes":      snapshot.MaxBytes,
		"truncated":      truncated,
		"redaction_note": "common secret fields and token patterns were redacted",
	})
}

func CaptureMessageTraceFromBodyStorage(ctx *gin.Context, storage common.BodyStorage) {
	if ctx == nil || storage == nil {
		return
	}
	snapshot := GetMessageTraceSnapshot()
	if !snapshot.Enabled {
		return
	}
	currentOffset, err := storage.Seek(0, io.SeekCurrent)
	if err != nil {
		return
	}
	if _, err = storage.Seek(0, io.SeekStart); err != nil {
		_, _ = storage.Seek(currentOffset, io.SeekStart)
		return
	}
	limit := snapshot.MaxBytes + 1
	if limit <= 1 {
		limit = defaultMessageTraceMaxBytes + 1
	}
	raw, err := io.ReadAll(io.LimitReader(storage, limit))
	_, _ = storage.Seek(currentOffset, io.SeekStart)
	if err != nil {
		return
	}
	body, truncated := sanitizeMessageTraceBody(raw, snapshot.MaxBytes)
	if !truncated && storage.Size() > int64(len(raw)) {
		truncated = true
	}
	ctx.Set(messageTraceContextKey, map[string]interface{}{
		"captured_at":    common.GetTimestamp(),
		"expires_at":     snapshot.Until,
		"operation":      messageTraceDefaultOperation,
		"body":           body,
		"body_bytes":     storage.Size(),
		"max_bytes":      snapshot.MaxBytes,
		"truncated":      truncated,
		"redaction_note": "common secret fields and token patterns were redacted",
	})
}

func attachMessageTraceFields(ctx *gin.Context, fields map[string]interface{}) {
	if ctx == nil || len(fields) == 0 {
		return
	}
	trace, ok := ctx.Get(messageTraceContextKey)
	if !ok || trace == nil {
		snapshot := GetMessageTraceSnapshot()
		trace = map[string]interface{}{
			"captured_at":    common.GetTimestamp(),
			"expires_at":     snapshot.Until,
			"operation":      messageTraceDefaultOperation,
			"max_bytes":      snapshot.MaxBytes,
			"redaction_note": "common secret fields and token patterns were redacted",
		}
		ctx.Set(messageTraceContextKey, trace)
	}
	traceMap, ok := trace.(map[string]interface{})
	if !ok {
		return
	}
	for key, value := range fields {
		traceMap[key] = value
	}
}

func AttachMessageTrace(ctx *gin.Context, adminInfo map[string]interface{}) {
	if ctx == nil || adminInfo == nil {
		return
	}
	trace, ok := ctx.Get(messageTraceContextKey)
	if !ok || trace == nil {
		return
	}
	adminInfo["message_trace"] = trace
}

func sanitizeMessageTraceBody(raw []byte, maxBytes int64) (string, bool) {
	if maxBytes <= 0 {
		maxBytes = defaultMessageTraceMaxBytes
	}
	originalLen := len(raw)
	truncated := int64(originalLen) > maxBytes
	if truncated {
		raw = raw[:maxBytes]
	}
	body := string(raw)
	body = secretFieldPattern.ReplaceAllString(body, `$1"<redacted>"`)
	body = bearerPattern.ReplaceAllString(body, `${1}<redacted>`)
	body = skTokenPattern.ReplaceAllString(body, "sk-<redacted>")
	if truncated {
		body += "\n...<truncated after " + strconv.Itoa(len(raw)) + " of " + strconv.Itoa(originalLen) + " bytes>..."
	}
	return body, truncated
}
