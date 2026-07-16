package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const (
	DefaultMiniMaxMaxTokensLimit = uint(24576)
	miniMaxResponseProbeMaxBytes = 64 << 10
)

var miniMaxUnsafeToolCallIDPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

type miniMaxEmbeddedErrorDetails struct {
	ErrorCode string `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
}

type miniMaxEmbeddedErrorResponse struct {
	Error     *miniMaxEmbeddedErrorDetails `json:"error"`
	ErrorCode string                       `json:"error_code"`
	ErrorMsg  string                       `json:"error_msg"`
}

type miniMaxEmbeddedErrorMessage struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Identity string `json:"identity"`
	Quota    int64  `json:"quota"`
	Used     int64  `json:"used"`
}

type miniMaxReplayResponseBody struct {
	io.Reader
	closer io.ReadCloser
}

func (b *miniMaxReplayResponseBody) Close() error {
	return b.closer.Close()
}

// ApplyMiniMaxOpenAIRequestCompatibility applies the strict request profile
// required by W3 MiniMax and compatible OpenAI-style MiniMax gateways.
func ApplyMiniMaxOpenAIRequestCompatibility(request *dto.GeneralOpenAIRequest) {
	if request == nil {
		return
	}
	request.StreamOptions = nil
	request.MaxCompletionTokens = nil
	request.TopK = nil
	request.Temperature = nil
	request.TopP = nil
	request.Stop = nil
	request.N = nil
	request.ResponseFormat = nil
	request.FrequencyPenalty = nil
	request.PresencePenalty = nil
	request.Seed = nil
	request.ParallelTooCalls = nil
	request.ToolChoice = nil
	request.FunctionCall = nil
	request.User = nil
	request.ServiceTier = nil
	request.LogProbs = nil
	request.TopLogProbs = nil
	request.Metadata = nil
	request.Reasoning = nil
	request.ReasoningEffort = ""
	request.ReasoningSplit = nil
	stripMiniMaxAnthropicBillingHeader(request)
	stripMiniMaxUnsupportedMessagePartFields(request)
	sanitizeMiniMaxRequestText(request)
	normalizeMiniMaxToolCallIDs(request)
	if request.MaxTokens != nil && *request.MaxTokens > DefaultMiniMaxMaxTokensLimit {
		request.MaxTokens = common.GetPointer(DefaultMiniMaxMaxTokensLimit)
	}
}

func stripMiniMaxAnthropicBillingHeader(request *dto.GeneralOpenAIRequest) {
	for i := range request.Messages {
		if request.Messages[i].Content == nil {
			continue
		}
		if request.Messages[i].IsStringContent() {
			request.Messages[i].SetStringContent(StripClaudeCodeBillingMetadataPrefix(request.Messages[i].StringContent()))
			continue
		}
		contents := request.Messages[i].ParseContent()
		changed := false
		for j := range contents {
			if contents[j].Type == dto.ContentTypeText {
				stripped := StripClaudeCodeBillingMetadataPrefix(contents[j].Text)
				if stripped != contents[j].Text {
					contents[j].Text = stripped
					changed = true
				}
			}
		}
		if changed {
			request.Messages[i].SetMediaContent(contents)
		}
	}
}

func stripMiniMaxUnsupportedMessagePartFields(request *dto.GeneralOpenAIRequest) {
	for i := range request.Messages {
		if request.Messages[i].Content == nil || request.Messages[i].IsStringContent() {
			continue
		}
		contents := request.Messages[i].ParseContent()
		changed := false
		for j := range contents {
			if contents[j].CacheControl != nil {
				contents[j].CacheControl = nil
				changed = true
			}
		}
		if changed {
			request.Messages[i].SetMediaContent(contents)
		}
	}
}

func sanitizeMiniMaxRequestText(request *dto.GeneralOpenAIRequest) {
	for i := range request.Messages {
		if request.Messages[i].Content != nil {
			if request.Messages[i].IsStringContent() {
				request.Messages[i].SetStringContent(sanitizeMiniMaxSurrogates(request.Messages[i].StringContent()))
			} else {
				contents := request.Messages[i].ParseContent()
				changed := false
				for j := range contents {
					if contents[j].Type == dto.ContentTypeText {
						sanitized := sanitizeMiniMaxSurrogates(contents[j].Text)
						if sanitized != contents[j].Text {
							contents[j].Text = sanitized
							changed = true
						}
					}
				}
				if changed {
					request.Messages[i].SetMediaContent(contents)
				}
			}
		}
		if request.Messages[i].ReasoningContent != nil {
			*request.Messages[i].ReasoningContent = sanitizeMiniMaxSurrogates(*request.Messages[i].ReasoningContent)
		}
		if request.Messages[i].Reasoning != nil {
			*request.Messages[i].Reasoning = sanitizeMiniMaxSurrogates(*request.Messages[i].Reasoning)
		}
	}
}

func sanitizeMiniMaxSurrogates(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0xD800 && r <= 0xDFFF {
			return -1
		}
		return r
	}, text)
}

func normalizeMiniMaxToolCallIDs(request *dto.GeneralOpenAIRequest) {
	for i := range request.Messages {
		if request.Messages[i].ToolCallId != "" {
			request.Messages[i].ToolCallId = normalizeMiniMaxToolCallID(request.Messages[i].ToolCallId)
		}
		if len(request.Messages[i].ToolCalls) == 0 {
			continue
		}
		var toolCalls []dto.ToolCallRequest
		if err := common.Unmarshal(request.Messages[i].ToolCalls, &toolCalls); err != nil {
			continue
		}
		changed := false
		for j := range toolCalls {
			normalized := normalizeMiniMaxToolCallID(toolCalls[j].ID)
			if normalized != toolCalls[j].ID {
				toolCalls[j].ID = normalized
				changed = true
			}
		}
		if changed {
			request.Messages[i].SetToolCalls(toolCalls)
		}
	}
}

func normalizeMiniMaxToolCallID(id string) string {
	if strings.Contains(id, "|") {
		id = strings.Split(id, "|")[0]
	}
	id = miniMaxUnsafeToolCallIDPattern.ReplaceAllString(id, "_")
	if len(id) > 40 {
		return id[:40]
	}
	return id
}

// StripClaudeCodeBillingMetadataPrefix removes the metadata prefix injected by
// Claude Code while preserving the actual prompt text.
func StripClaudeCodeBillingMetadataPrefix(content string) string {
	trimmedLeft := strings.TrimLeft(content, " \t\r\n")
	original := trimmedLeft
	const claudeCodePromptMarker = "You are Claude Code"
	if markerIdx := strings.Index(trimmedLeft, claudeCodePromptMarker); markerIdx > 0 {
		prefixText := strings.ToLower(trimmedLeft[:markerIdx])
		if strings.Contains(prefixText, "x-anthropic-billing-header:") ||
			strings.Contains(prefixText, "cc_version=") ||
			strings.Contains(prefixText, "cc_entrypoint=") ||
			strings.Contains(prefixText, "cch=") {
			return trimmedLeft[markerIdx:]
		}
	}
	const prefix = "x-anthropic-billing-header:"
	if strings.HasPrefix(strings.ToLower(trimmedLeft), prefix) {
		trimmedLeft = strings.TrimLeft(trimmedLeft[len(prefix):], " \t\r\n")
	}
	for {
		lower := strings.ToLower(trimmedLeft)
		if !strings.HasPrefix(lower, "cc_version=") &&
			!strings.HasPrefix(lower, "cc_entrypoint=") &&
			!strings.HasPrefix(lower, "cch=") {
			break
		}
		idx := strings.Index(trimmedLeft, ";")
		if idx < 0 {
			break
		}
		trimmedLeft = strings.TrimLeft(trimmedLeft[idx+1:], " \t\r\n")
	}
	if trimmedLeft != original {
		return trimmedLeft
	}
	return content
}

// StripClaudeCodeBillingMetadata removes Claude Code billing metadata from
// string and text-block content in a Claude request.
func StripClaudeCodeBillingMetadata(request *dto.ClaudeRequest) {
	if request == nil {
		return
	}

	if request.System != nil {
		if request.IsStringSystem() {
			request.SetStringSystem(StripClaudeCodeBillingMetadataPrefix(request.GetStringSystem()))
		} else {
			system := request.ParseSystem()
			changed := false
			for i := range system {
				if system[i].Type != dto.ContentTypeText || system[i].Text == nil {
					continue
				}
				stripped := StripClaudeCodeBillingMetadataPrefix(system[i].GetText())
				if stripped != system[i].GetText() {
					system[i].SetText(stripped)
					changed = true
				}
			}
			if changed {
				request.System = system
			}
		}
	}

	for i := range request.Messages {
		if request.Messages[i].Content == nil {
			continue
		}
		if request.Messages[i].IsStringContent() {
			request.Messages[i].SetStringContent(
				StripClaudeCodeBillingMetadataPrefix(request.Messages[i].GetStringContent()),
			)
			continue
		}
		contents, err := request.Messages[i].ParseContent()
		if err != nil {
			continue
		}
		changed := false
		for j := range contents {
			if contents[j].Type != dto.ContentTypeText || contents[j].Text == nil {
				continue
			}
			stripped := StripClaudeCodeBillingMetadataPrefix(contents[j].GetText())
			if stripped != contents[j].GetText() {
				contents[j].SetText(stripped)
				changed = true
			}
		}
		if changed {
			request.Messages[i].SetContent(contents)
		}
	}
}

// ValidateMiniMaxCompatibilityResponse recognizes MiniMax errors that are
// embedded in HTTP 200 JSON or the first SSE data record, making them visible
// to the normal relay retry policy.
func ValidateMiniMaxCompatibilityResponse(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*http.Response, error) {
	embeddedErr, err := InspectMiniMaxEmbeddedErrorResponse(resp)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("inspect MiniMax response failed: %w", err)
	}
	if embeddedErr == nil {
		return resp, nil
	}

	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	channelID := 0
	if info != nil && info.ChannelMeta != nil {
		channelID = info.ChannelId
	}
	logContext := context.Background()
	if c != nil && c.Request != nil {
		logContext = c.Request.Context()
	}
	logger.LogWarn(logContext, fmt.Sprintf(
		"MiniMax embedded error: channel_id=%d normalized_status=%d error_code=%s",
		channelID,
		embeddedErr.StatusCode,
		embeddedErr.GetErrorCode(),
	))
	return nil, embeddedErr
}

func InspectMiniMaxEmbeddedErrorResponse(resp *http.Response) (*types.NewAPIError, error) {
	if resp == nil || resp.Body == nil || resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	probe, err := readMiniMaxResponseProbe(resp)
	if err != nil {
		return nil, err
	}
	probe = bytes.TrimSpace(probe)
	if len(probe) == 0 || bytes.Equal(probe, []byte("[DONE]")) {
		return nil, nil
	}

	var embedded miniMaxEmbeddedErrorResponse
	if err := common.Unmarshal(probe, &embedded); err != nil {
		return nil, nil
	}

	code := strings.TrimSpace(embedded.ErrorCode)
	message := strings.TrimSpace(embedded.ErrorMsg)
	if embedded.Error != nil {
		if code == "" {
			code = strings.TrimSpace(embedded.Error.ErrorCode)
		}
		if message == "" {
			message = strings.TrimSpace(embedded.Error.ErrorMsg)
		}
	}
	if (code == "" || code == "0" || strings.EqualFold(code, "success")) && message == "" {
		return nil, nil
	}
	if code == "" || code == "0" || strings.EqualFold(code, "success") {
		code = "minimax_embedded_error"
	}

	statusCode := miniMaxEmbeddedErrorStatusCode(code, message)
	return types.WithOpenAIError(types.OpenAIError{
		Message: formatMiniMaxEmbeddedErrorMessage(code, message),
		Type:    "upstream_error",
		Code:    code,
	}, statusCode), nil
}

func readMiniMaxResponseProbe(resp *http.Response) ([]byte, error) {
	originalBody := resp.Body
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if !strings.HasPrefix(contentType, "text/event-stream") {
		body, err := io.ReadAll(originalBody)
		if err != nil {
			resp.Body = &miniMaxReplayResponseBody{
				Reader: io.MultiReader(bytes.NewReader(body), originalBody),
				closer: originalBody,
			}
			return nil, err
		}
		resp.Body = &miniMaxReplayResponseBody{Reader: bytes.NewReader(body), closer: originalBody}
		return body, nil
	}

	reader := bufio.NewReader(originalBody)
	prefix := bytes.NewBuffer(nil)
	var candidate []byte
	for prefix.Len() < miniMaxResponseProbeMaxBytes {
		line, err := reader.ReadString('\n')
		prefix.WriteString(line)
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "data:"):
			candidate = []byte(strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
		case trimmed == "", strings.HasPrefix(trimmed, ":"), strings.HasPrefix(trimmed, "event:"), strings.HasPrefix(trimmed, "id:"), strings.HasPrefix(trimmed, "retry:"):
			// Continue through SSE metadata until the first data record.
		default:
			candidate = []byte(trimmed)
		}

		if len(candidate) > 0 || err != nil {
			if err != nil && !errors.Is(err, io.EOF) {
				resp.Body = &miniMaxReplayResponseBody{
					Reader: io.MultiReader(bytes.NewReader(prefix.Bytes()), reader),
					closer: originalBody,
				}
				return nil, err
			}
			break
		}
	}

	resp.Body = &miniMaxReplayResponseBody{
		Reader: io.MultiReader(bytes.NewReader(prefix.Bytes()), reader),
		closer: originalBody,
	}
	return candidate, nil
}

func miniMaxEmbeddedErrorStatusCode(code string, message string) int {
	if idx := strings.LastIndex(code, "."); idx >= 0 && idx+1 < len(code) {
		if statusCode, err := strconv.Atoi(code[idx+1:]); err == nil && statusCode >= 400 && statusCode <= 599 {
			return statusCode
		}
	}
	lower := strings.ToLower(code + " " + message)
	if strings.Contains(lower, "quota exceeded") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests") {
		return http.StatusTooManyRequests
	}
	return http.StatusBadGateway
}

func formatMiniMaxEmbeddedErrorMessage(code string, message string) string {
	description := strings.TrimSpace(message)
	var details miniMaxEmbeddedErrorMessage
	if description != "" && common.UnmarshalJsonStr(description, &details) == nil && details.Message != "" {
		description = details.Message
		metadata := make([]string, 0, 4)
		if details.Type != "" {
			metadata = append(metadata, "type="+details.Type)
		}
		if details.Identity != "" {
			metadata = append(metadata, "identity="+details.Identity)
		}
		if details.Quota > 0 {
			metadata = append(metadata, fmt.Sprintf("quota=%d", details.Quota))
		}
		if details.Used > 0 {
			metadata = append(metadata, fmt.Sprintf("used=%d", details.Used))
		}
		if len(metadata) > 0 {
			description += " (" + strings.Join(metadata, ", ") + ")"
		}
	}
	if description == "" {
		description = "upstream request failed"
	}
	return fmt.Sprintf("MiniMax upstream error %s: %s", code, common.MaskSensitiveInfo(description))
}
