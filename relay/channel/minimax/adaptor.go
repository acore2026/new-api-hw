package minimax

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
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/samber/lo"
)

type Adaptor struct {
}

const (
	defaultW3MaxTokensLimit = uint(24576)
	w3ResponseProbeMaxBytes = 64 << 10
)

var w3UnsafeToolCallIDPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

type w3EmbeddedErrorDetails struct {
	ErrorCode string `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
}

type w3EmbeddedErrorResponse struct {
	Error     *w3EmbeddedErrorDetails `json:"error"`
	ErrorCode string                  `json:"error_code"`
	ErrorMsg  string                  `json:"error_msg"`
}

type w3EmbeddedErrorMessage struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Identity string `json:"identity"`
	Quota    int64  `json:"quota"`
	Used     int64  `json:"used"`
}

type w3ReplayResponseBody struct {
	io.Reader
	closer io.ReadCloser
}

func (b *w3ReplayResponseBody) Close() error {
	return b.closer.Close()
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.ClaudeRequest) (any, error) {
	if isW3OAuthEnabled(info) {
		openAIRequest, err := service.ClaudeToOpenAIRequest(*req, info)
		if err != nil {
			return nil, err
		}
		return a.ConvertOpenAIRequest(c, info, openAIRequest)
	}
	adaptor := claude.Adaptor{}
	return adaptor.ConvertClaudeRequest(c, info, req)
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	if isW3OAuthEnabled(info) {
		return nil, errors.New("w3 minimax only supports chat completions")
	}
	if info.RelayMode != constant.RelayModeAudioSpeech {
		return nil, errors.New("unsupported audio relay mode")
	}

	voiceID := request.Voice
	speed := lo.FromPtrOr(request.Speed, 0.0)
	outputFormat := request.ResponseFormat

	minimaxRequest := MiniMaxTTSRequest{
		Model: info.OriginModelName,
		Text:  request.Input,
		VoiceSetting: VoiceSetting{
			VoiceID: voiceID,
			Speed:   speed,
		},
		AudioSetting: &AudioSetting{
			Format: outputFormat,
		},
		OutputFormat: outputFormat,
	}

	// 同步扩展字段的厂商自定义metadata
	if len(request.Metadata) > 0 {
		if err := common.Unmarshal(request.Metadata, &minimaxRequest); err != nil {
			return nil, fmt.Errorf("error unmarshalling metadata to minimax request: %w", err)
		}
	}

	jsonData, err := common.Marshal(minimaxRequest)
	if err != nil {
		return nil, fmt.Errorf("error marshalling minimax request: %w", err)
	}
	if outputFormat != "hex" {
		outputFormat = "url"
	}

	c.Set("response_format", outputFormat)

	// Debug: log the request structure
	// fmt.Printf("MiniMax TTS Request: %s\n", string(jsonData))

	return bytes.NewReader(jsonData), nil
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if isW3OAuthEnabled(info) {
		return nil, errors.New("w3 minimax only supports chat completions")
	}
	if info.RelayMode != constant.RelayModeImagesGenerations {
		return nil, fmt.Errorf("unsupported image relay mode: %d", info.RelayMode)
	}
	return oaiImage2MiniMaxImageRequest(request), nil
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return GetRequestURL(info)
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	if isW3OAuthEnabled(info) {
		oauthKey, err := ensureW3OAuthKey(c, info)
		if err != nil {
			return err
		}
		config := service.ResolveW3OAuthConfig(info.ChannelOtherSettings)
		req.Del("Authorization")
		req.Set("Content-Type", "application/json")
		if info.IsStream {
			req.Set("Accept", "text/event-stream")
		} else {
			req.Set("Accept", "application/json")
		}
		req.Set("User-Agent", "claude-proxy/1.1.1")
		req.Set("X-Auth-Token", strings.TrimSpace(oauthKey.AccessToken))
		req.Set("X-Provider-ID", config.ProviderID)
		req.Set("X-Request-ID", uuid.New().String())
		return nil
	}
	req.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if isW3OAuthEnabled(info) {
		if !isW3ChatRelay(info) {
			return nil, errors.New("w3 minimax only supports chat completions")
		}
		info.FinalRequestRelayFormat = types.RelayFormatOpenAI
		applyW3OpenAIRequestCompatibility(request)
		return request, nil
	}
	return request, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if isW3OAuthEnabled(info) {
		body, err := io.ReadAll(requestBody)
		if err != nil {
			return nil, fmt.Errorf("read w3 request body failed: %w", err)
		}
		service.CaptureFinalMessageTrace(c, body)
		resp, err := doW3RequestOnce(a, c, info, body)
		if err != nil {
			return nil, err
		}
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && info.ChannelId > 0 && !info.ChannelIsMultiKey {
			refreshCtx, cancel := contextWithRequestTimeout(c, 15)
			newKey, _, refreshErr := service.RefreshW3ChannelCredential(refreshCtx, info.ChannelId, service.W3CredentialRefreshOptions{Force: true, ResetCaches: true})
			cancel()
			if refreshErr != nil {
				return resp, nil
			}
			_ = resp.Body.Close()
			if encoded, encodeErr := service.EncodeW3OAuthKey(newKey); encodeErr == nil {
				info.ApiKey = encoded
			}
			resp, err = doW3RequestOnce(a, c, info, body)
			if err != nil {
				return nil, err
			}
		}
		return validateW3Response(c, info, resp)
	}
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.RelayMode == constant.RelayModeAudioSpeech {
		return handleTTSResponse(c, resp, info)
	}
	if info.RelayMode == constant.RelayModeImagesGenerations {
		return miniMaxImageHandler(c, resp, info)
	}

	if isW3OAuthEnabled(info) {
		adaptor := openai.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	}

	switch info.RelayFormat {
	case types.RelayFormatClaude:
		adaptor := claude.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	default:
		adaptor := openai.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	}
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

func isW3OAuthEnabled(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelMeta != nil && info.ChannelOtherSettings.W3OAuthEnabled
}

func isW3ChatRelay(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	return info.RelayMode == constant.RelayModeChatCompletions || info.RelayFormat == types.RelayFormatClaude
}

func applyW3OpenAIRequestCompatibility(request *dto.GeneralOpenAIRequest) {
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
	stripW3AnthropicBillingHeader(request)
	stripW3UnsupportedMessagePartFields(request)
	sanitizeW3RequestText(request)
	normalizeW3ToolCallIDs(request)
	if request.MaxTokens != nil {
		if *request.MaxTokens > defaultW3MaxTokensLimit {
			request.MaxTokens = common.GetPointer(defaultW3MaxTokensLimit)
		}
	}
}

func stripW3AnthropicBillingHeader(request *dto.GeneralOpenAIRequest) {
	if request == nil {
		return
	}
	for i := range request.Messages {
		if request.Messages[i].Content == nil {
			continue
		}
		if request.Messages[i].IsStringContent() {
			request.Messages[i].SetStringContent(stripW3AnthropicBillingHeaderPrefix(request.Messages[i].StringContent()))
			continue
		}
		contents := request.Messages[i].ParseContent()
		changed := false
		for j := range contents {
			if contents[j].Type == dto.ContentTypeText {
				stripped := stripW3AnthropicBillingHeaderPrefix(contents[j].Text)
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

func stripW3UnsupportedMessagePartFields(request *dto.GeneralOpenAIRequest) {
	if request == nil {
		return
	}
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

func sanitizeW3RequestText(request *dto.GeneralOpenAIRequest) {
	if request == nil {
		return
	}
	for i := range request.Messages {
		if request.Messages[i].Content != nil {
			if request.Messages[i].IsStringContent() {
				request.Messages[i].SetStringContent(sanitizeW3Surrogates(request.Messages[i].StringContent()))
			} else {
				contents := request.Messages[i].ParseContent()
				changed := false
				for j := range contents {
					if contents[j].Type == dto.ContentTypeText {
						sanitized := sanitizeW3Surrogates(contents[j].Text)
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
			*request.Messages[i].ReasoningContent = sanitizeW3Surrogates(*request.Messages[i].ReasoningContent)
		}
		if request.Messages[i].Reasoning != nil {
			*request.Messages[i].Reasoning = sanitizeW3Surrogates(*request.Messages[i].Reasoning)
		}
	}
}

func sanitizeW3Surrogates(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0xD800 && r <= 0xDFFF {
			return -1
		}
		return r
	}, text)
}

func normalizeW3ToolCallIDs(request *dto.GeneralOpenAIRequest) {
	if request == nil {
		return
	}
	for i := range request.Messages {
		if request.Messages[i].ToolCallId != "" {
			request.Messages[i].ToolCallId = normalizeW3ToolCallID(request.Messages[i].ToolCallId)
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
			normalized := normalizeW3ToolCallID(toolCalls[j].ID)
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

func normalizeW3ToolCallID(id string) string {
	if strings.Contains(id, "|") {
		id = strings.Split(id, "|")[0]
	}
	id = w3UnsafeToolCallIDPattern.ReplaceAllString(id, "_")
	if len(id) > 40 {
		return id[:40]
	}
	return id
}

func stripW3AnthropicBillingHeaderPrefix(content string) string {
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

func ensureW3OAuthKey(c *gin.Context, info *relaycommon.RelayInfo) (*service.W3OAuthKey, error) {
	oauthKey, err := service.ParseW3OAuthKey(strings.TrimSpace(info.ApiKey))
	if err != nil {
		return nil, err
	}
	if info.ChannelId <= 0 || info.ChannelIsMultiKey || !service.W3OAuthKeyExpiresWithin(oauthKey, time.Now(), service.W3CredentialRefreshThreshold) {
		return oauthKey, nil
	}

	refreshCtx, cancel := contextWithRequestTimeout(c, 15)
	defer cancel()
	refreshedKey, _, err := service.RefreshW3ChannelCredential(refreshCtx, info.ChannelId, service.W3CredentialRefreshOptions{ResetCaches: true})
	if err != nil {
		return nil, err
	}
	if encoded, err := service.EncodeW3OAuthKey(refreshedKey); err == nil {
		info.ApiKey = encoded
	}
	return refreshedKey, nil
}

func doW3RequestOnce(a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo, requestBody []byte) (*http.Response, error) {
	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", err)
	}
	if common.DebugEnabled {
		println("fullRequestURL:", fullRequestURL)
	}

	req, err := http.NewRequest(c.Request.Method, fullRequestURL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", err)
	}
	headers := req.Header
	if err := a.SetupRequestHeader(c, &headers, info); err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	headerOverride, err := channel.ResolveHeaderOverride(info, c)
	if err != nil {
		return nil, err
	}
	for key, value := range headerOverride {
		req.Header.Set(key, value)
		if strings.EqualFold(key, "Host") {
			req.Host = value
		}
	}
	if info.IsStream {
		helper.SetEventStreamHeaders(c)
	}

	config := service.ResolveW3OAuthConfig(info.ChannelOtherSettings)
	if info.ChannelSetting.TLSInsecureSkipVerify {
		config.VerifyTLS = false
	}
	requestTimeout := time.Duration(0)
	if info.IsStream {
		requestTimeout = -1
	}
	client, err := service.GetW3HTTPClient(info.ChannelSetting.Proxy, config.VerifyTLS, requestTimeout)
	if err != nil {
		return nil, fmt.Errorf("new w3 http client failed: %w", err)
	}
	logger.LogDebug(c.Request.Context(), fmt.Sprintf("w3 minimax request: channel_id=%d url=%s stream=%t verify_tls=%t provider_id=%s proxy=disabled", info.ChannelId, fullRequestURL, info.IsStream, config.VerifyTLS, config.ProviderID))
	resp, err := client.Do(req)
	if err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("w3 minimax request failed: channel_id=%d url=%s error=%v", info.ChannelId, fullRequestURL, err))
		return nil, fmt.Errorf("do request failed: %w", err)
	}
	if resp == nil {
		return nil, errors.New("resp is nil")
	}
	service.WrapUpstreamResponseTrace(c, resp)
	logger.LogDebug(c.Request.Context(), fmt.Sprintf(
		"w3 minimax response: channel_id=%d url=%s status=%d content_type=%q content_length=%d",
		info.ChannelId,
		fullRequestURL,
		resp.StatusCode,
		resp.Header.Get("Content-Type"),
		resp.ContentLength,
	))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		logW3MinimaxBadResponse(c, info, fullRequestURL, resp)
	}
	if upID := resp.Header.Get(common.RequestIdKey); upID != "" {
		c.Set(common.UpstreamRequestIdKey, upID)
	}
	_ = req.Body.Close()
	return resp, nil
}

func logW3MinimaxBadResponse(c *gin.Context, info *relaycommon.RelayInfo, fullRequestURL string, resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("w3 minimax bad response read failed: channel_id=%d url=%s status=%d error=%v", info.ChannelId, fullRequestURL, resp.StatusCode, err))
		return
	}
	resp.Body = io.NopCloser(bytes.NewReader(responseBody))
	logger.LogWarn(c.Request.Context(), fmt.Sprintf(
		"w3 minimax bad response: channel_id=%d url=%s status=%d response_bytes=%d",
		info.ChannelId,
		fullRequestURL,
		resp.StatusCode,
		len(responseBody),
	))
}

func validateW3Response(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*http.Response, error) {
	embeddedErr, err := inspectW3EmbeddedErrorResponse(resp)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("inspect w3 response failed: %w", err)
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
		"w3 minimax embedded error: channel_id=%d normalized_status=%d error_code=%s",
		channelID,
		embeddedErr.StatusCode,
		embeddedErr.GetErrorCode(),
	))
	return nil, embeddedErr
}

func inspectW3EmbeddedErrorResponse(resp *http.Response) (*types.NewAPIError, error) {
	if resp == nil || resp.Body == nil || resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	probe, err := readW3ResponseProbe(resp)
	if err != nil {
		return nil, err
	}
	probe = bytes.TrimSpace(probe)
	if len(probe) == 0 || bytes.Equal(probe, []byte("[DONE]")) {
		return nil, nil
	}

	var embedded w3EmbeddedErrorResponse
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
		code = "w3_embedded_error"
	}

	statusCode := w3EmbeddedErrorStatusCode(code, message)
	return types.WithOpenAIError(types.OpenAIError{
		Message: formatW3EmbeddedErrorMessage(code, message),
		Type:    "upstream_error",
		Code:    code,
	}, statusCode), nil
}

func readW3ResponseProbe(resp *http.Response) ([]byte, error) {
	originalBody := resp.Body
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if !strings.HasPrefix(contentType, "text/event-stream") {
		body, err := io.ReadAll(originalBody)
		if err != nil {
			resp.Body = &w3ReplayResponseBody{
				Reader: io.MultiReader(bytes.NewReader(body), originalBody),
				closer: originalBody,
			}
			return nil, err
		}
		resp.Body = &w3ReplayResponseBody{Reader: bytes.NewReader(body), closer: originalBody}
		return body, nil
	}

	reader := bufio.NewReader(originalBody)
	prefix := bytes.NewBuffer(nil)
	var candidate []byte
	for prefix.Len() < w3ResponseProbeMaxBytes {
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
				resp.Body = &w3ReplayResponseBody{
					Reader: io.MultiReader(bytes.NewReader(prefix.Bytes()), reader),
					closer: originalBody,
				}
				return nil, err
			}
			break
		}
	}

	resp.Body = &w3ReplayResponseBody{
		Reader: io.MultiReader(bytes.NewReader(prefix.Bytes()), reader),
		closer: originalBody,
	}
	return candidate, nil
}

func w3EmbeddedErrorStatusCode(code string, message string) int {
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

func formatW3EmbeddedErrorMessage(code string, message string) string {
	description := strings.TrimSpace(message)
	var details w3EmbeddedErrorMessage
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
	return fmt.Sprintf("W3 upstream error %s: %s", code, common.MaskSensitiveInfo(description))
}

func contextWithRequestTimeout(c *gin.Context, seconds int) (context.Context, context.CancelFunc) {
	if c != nil && c.Request != nil {
		return context.WithTimeout(c.Request.Context(), time.Duration(seconds)*time.Second)
	}
	return context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
}
