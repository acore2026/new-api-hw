package minimax

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	channelconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func TestGetRequestURLForImageGeneration(t *testing.T) {
	t.Parallel()

	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.minimax.chat",
		},
	}

	got, err := GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}

	want := "https://api.minimax.chat/v1/image_generation"
	if got != want {
		t.Fatalf("GetRequestURL() = %q, want %q", got, want)
	}
}

func TestGetRequestURLForW3ChatCompletions(t *testing.T) {
	t.Parallel()

	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{
				W3OAuthEnabled: true,
				W3ApiBaseURL:   "https://codeagent.example.com/codeAgentPro",
			},
		},
	}

	got, err := GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}

	want := "https://codeagent.example.com/codeAgentPro/chat/completions"
	if got != want {
		t.Fatalf("GetRequestURL() = %q, want %q", got, want)
	}
}

func TestGetRequestURLForW3ClaudeMessagesUsesChatCompletions(t *testing.T) {
	t.Parallel()

	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeUnknown,
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{
				W3OAuthEnabled: true,
				W3ApiBaseURL:   "https://codeagent.example.com/codeAgentPro",
			},
		},
	}

	got, err := GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}

	want := "https://codeagent.example.com/codeAgentPro/chat/completions"
	if got != want {
		t.Fatalf("GetRequestURL() = %q, want %q", got, want)
	}
}

func TestSetupRequestHeaderForW3UsesOAuthHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	key, err := service.EncodeW3OAuthKey(&service.W3OAuthKey{
		Type:         "w3",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		Expired:      time.Now().Add(time.Hour).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("EncodeW3OAuthKey returned error: %v", err)
	}

	info := &relaycommon.RelayInfo{
		IsStream: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: key,
			ChannelOtherSettings: dto.ChannelOtherSettings{
				W3OAuthEnabled: true,
				W3ProviderID:   "hw-minimax",
			},
		},
	}

	header := http.Header{}
	adaptor := &Adaptor{}
	if err := adaptor.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}

	if got := header.Get("X-Auth-Token"); got != "access-token" {
		t.Fatalf("X-Auth-Token = %q, want access-token", got)
	}
	if got := header.Get("X-Provider-ID"); got != "hw-minimax" {
		t.Fatalf("X-Provider-ID = %q, want hw-minimax", got)
	}
	if got := header.Get("User-Agent"); got != "claude-proxy/1.1.1" {
		t.Fatalf("User-Agent = %q, want claude-proxy/1.1.1", got)
	}
	if got := header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept = %q, want text/event-stream", got)
	}
	if got := header.Get("X-Request-ID"); got == "" || !strings.Contains(got, "-") {
		t.Fatalf("X-Request-ID = %q, want dashed UUID request id", got)
	}
	if got := header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
}

func TestConvertOpenAIRequestForW3RemovesStreamOptions(t *testing.T) {
	t.Parallel()

	topK := 1
	request := &dto.GeneralOpenAIRequest{
		Model:         "MiniMax-M2.7",
		StreamOptions: &dto.StreamOptions{IncludeUsage: true},
		TopK:          &topK,
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{W3OAuthEnabled: true},
		},
	}

	got, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	converted := got.(*dto.GeneralOpenAIRequest)
	if converted.StreamOptions != nil {
		t.Fatalf("StreamOptions = %#v, want nil", converted.StreamOptions)
	}
	if converted.TopK != nil {
		t.Fatalf("TopK = %#v, want nil", converted.TopK)
	}
	if got := info.GetFinalRequestRelayFormat(); got != types.RelayFormatOpenAI {
		t.Fatalf("final request relay format = %q, want %q", got, types.RelayFormatOpenAI)
	}
}

func TestConvertOpenAIRequestForW3CapsMaxTokens(t *testing.T) {
	t.Parallel()

	request := &dto.GeneralOpenAIRequest{
		Model:     "MiniMax-M2.7",
		MaxTokens: common.GetPointer(uint(32000)),
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{W3OAuthEnabled: true},
		},
	}

	got, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	converted := got.(*dto.GeneralOpenAIRequest)
	if converted.MaxTokens == nil || *converted.MaxTokens != defaultW3MaxTokensLimit {
		t.Fatalf("MaxTokens = %#v, want %d", converted.MaxTokens, defaultW3MaxTokensLimit)
	}
}

func TestConvertOpenAIRequestForW3PreservesLowMaxTokens(t *testing.T) {
	t.Parallel()

	request := &dto.GeneralOpenAIRequest{
		Model:     "MiniMax-M2.7",
		MaxTokens: common.GetPointer(uint(1)),
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{W3OAuthEnabled: true},
		},
	}

	got, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	converted := got.(*dto.GeneralOpenAIRequest)
	if converted.MaxTokens == nil || *converted.MaxTokens != 1 {
		t.Fatalf("MaxTokens = %#v, want 1", converted.MaxTokens)
	}
}

func TestConvertOpenAIRequestForW3StripsUnsupportedFields(t *testing.T) {
	t.Parallel()

	temperature := 0.3
	topP := 0.9
	n := 2
	logprobs := true
	request := &dto.GeneralOpenAIRequest{
		Model:               "MiniMax-M2.7",
		StreamOptions:       &dto.StreamOptions{IncludeUsage: true},
		MaxTokens:           common.GetPointer(uint(4096)),
		MaxCompletionTokens: common.GetPointer(uint(4096)),
		Temperature:         &temperature,
		TopP:                &topP,
		Stop:                []string{"stop"},
		N:                   &n,
		LogProbs:            &logprobs,
		ReasoningEffort:     "high",
		ToolChoice:          "auto",
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{W3OAuthEnabled: true},
		},
	}

	got, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	converted := got.(*dto.GeneralOpenAIRequest)
	if converted.StreamOptions != nil || converted.MaxCompletionTokens != nil || converted.Temperature != nil || converted.TopP != nil ||
		converted.Stop != nil || converted.N != nil || converted.LogProbs != nil || converted.ReasoningEffort != "" || converted.ToolChoice != nil {
		t.Fatalf("unsupported fields were not stripped: %#v", converted)
	}
}

func TestConvertOpenAIRequestForW3StripsAnthropicBillingHeader(t *testing.T) {
	t.Parallel()

	request := &dto.GeneralOpenAIRequest{
		Model: "MiniMax-M2.7",
		Messages: []dto.Message{
			{Role: "system", Content: "x-anthropic-billing-header: cc_version=2.1.160.299; cc_entrypoint=cli; cch=cc342;You are Claude Code"},
			{Role: "user", Content: "keep this"},
			{Role: "assistant", Content: "cc_entrypoint=cli; cch=cc342;assistant content"},
		},
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{W3OAuthEnabled: true},
		},
	}

	got, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	converted := got.(*dto.GeneralOpenAIRequest)
	if gotContent := converted.Messages[0].StringContent(); gotContent != "You are Claude Code" {
		t.Fatalf("first message content = %q, want stripped Claude Code prompt", gotContent)
	}
	if gotContent := converted.Messages[1].StringContent(); gotContent != "keep this" {
		t.Fatalf("second message content = %q, want unchanged", gotContent)
	}
	if gotContent := converted.Messages[2].StringContent(); gotContent != "assistant content" {
		t.Fatalf("third message content = %q, want stripped partial Claude Code metadata", gotContent)
	}
}

func TestConvertOpenAIRequestForW3StripsCacheControl(t *testing.T) {
	t.Parallel()

	request := &dto.GeneralOpenAIRequest{
		Model: "MiniMax-M2.7",
		Messages: []dto.Message{
			{Role: "user"},
		},
	}
	request.Messages[0].SetMediaContent([]dto.MediaContent{
		{Type: dto.ContentTypeText, Text: "hello", CacheControl: json.RawMessage(`{"type":"ephemeral"}`)},
	})
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{W3OAuthEnabled: true},
		},
	}

	got, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	converted := got.(*dto.GeneralOpenAIRequest)
	contents := converted.Messages[0].ParseContent()
	if len(contents) != 1 {
		t.Fatalf("content parts = %#v, want one text part", contents)
	}
	if contents[0].CacheControl != nil {
		t.Fatalf("cache_control = %s, want nil", string(contents[0].CacheControl))
	}
}

func TestConvertOpenAIRequestForW3NormalizesToolCallIDs(t *testing.T) {
	t.Parallel()

	assistant := dto.Message{Role: "assistant", Content: nil}
	assistant.SetToolCalls([]dto.ToolCallRequest{
		{
			ID:   "call_abc|tool.name.with.extra.characters.and-too-long-for-w3",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "do_work",
				Arguments: "{}",
			},
		},
	})
	request := &dto.GeneralOpenAIRequest{
		Model: "MiniMax-M2.7",
		Messages: []dto.Message{
			assistant,
			{Role: "tool", ToolCallId: "call_abc|tool.name.with.extra.characters.and-too-long-for-w3", Content: "done"},
		},
	}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{W3OAuthEnabled: true},
		},
	}

	got, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	converted := got.(*dto.GeneralOpenAIRequest)
	if converted.Messages[1].ToolCallId != "call_abc" {
		t.Fatalf("tool_call_id = %q, want call_abc", converted.Messages[1].ToolCallId)
	}
	calls := converted.Messages[0].ParseToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_abc" {
		t.Fatalf("tool calls = %#v, want normalized call_abc id", calls)
	}
}

func TestConvertClaudeRequestForW3UsesOpenAIRequest(t *testing.T) {
	t.Parallel()

	maxTokens := uint(32)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeUnknown,
		RelayFormat:     types.RelayFormatClaude,
		OriginModelName: "MiniMax-M2.7",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: channelconstant.ChannelTypeMiniMax,
			ChannelOtherSettings: dto.ChannelOtherSettings{
				W3OAuthEnabled: true,
			},
		},
	}
	request := &dto.ClaudeRequest{
		Model:     "MiniMax-M2.7",
		MaxTokens: &maxTokens,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
	}

	got, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertClaudeRequest returned error: %v", err)
	}
	converted, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("converted request type = %T, want *dto.GeneralOpenAIRequest", got)
	}
	if converted.Model != request.Model {
		t.Fatalf("model = %q, want %q", converted.Model, request.Model)
	}
	if len(converted.Messages) != 1 || converted.Messages[0].Role != "user" {
		t.Fatalf("messages = %#v, want converted user message", converted.Messages)
	}
	if got := info.GetFinalRequestRelayFormat(); got != types.RelayFormatOpenAI {
		t.Fatalf("final request relay format = %q, want %q", got, types.RelayFormatOpenAI)
	}
}

func TestLogW3MinimaxBadResponsePreservesResponseBody(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       ioNopCloser(`{"error":{"message":"upstream failed"}}`),
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 2}}

	logW3MinimaxBadResponse(c, info, "https://codeagent.example.com/codeAgentPro/chat/completions", resp)

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	want := `{"error":{"message":"upstream failed"}}`
	if string(got) != want {
		t.Fatalf("response body after logging = %q, want %q", string(got), want)
	}
}

func TestConvertImageRequest(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: "image-01",
	}
	request := dto.ImageRequest{
		Model:          "image-01",
		Prompt:         "a red fox in snowfall",
		Size:           "1536x1024",
		ResponseFormat: "url",
		N:              uintPtr(2),
	}

	got, err := adaptor.ConvertImageRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if payload["model"] != "image-01" {
		t.Fatalf("model = %#v, want %q", payload["model"], "image-01")
	}
	if payload["prompt"] != request.Prompt {
		t.Fatalf("prompt = %#v, want %q", payload["prompt"], request.Prompt)
	}
	if payload["n"] != float64(2) {
		t.Fatalf("n = %#v, want 2", payload["n"])
	}
	if payload["aspect_ratio"] != "3:2" {
		t.Fatalf("aspect_ratio = %#v, want %q", payload["aspect_ratio"], "3:2")
	}
	if payload["response_format"] != "url" {
		t.Fatalf("response_format = %#v, want %q", payload["response_format"], "url")
	}
}

func TestDoResponseForImageGeneration(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		StartTime: time.Unix(1700000000, 0),
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       httptest.NewRecorder().Result().Body,
	}
	resp.Body = ioNopCloser(`{"data":{"image_urls":["https://example.com/minimax.png"]}}`)

	adaptor := &Adaptor{}
	usage, err := adaptor.DoResponse(c, resp, info)
	if err != nil {
		t.Fatalf("DoResponse returned error: %v", err)
	}
	if usage == nil {
		t.Fatalf("DoResponse returned nil usage")
	}

	body := recorder.Body.String()
	if !strings.Contains(body, `"url":"https://example.com/minimax.png"`) {
		t.Fatalf("response body = %s, want OpenAI image response with image URL", body)
	}
	if strings.Contains(body, `"image_urls"`) {
		t.Fatalf("response body = %s, should not expose raw MiniMax image_urls payload", body)
	}
}

type nopReadCloser struct {
	*strings.Reader
}

func (n nopReadCloser) Close() error {
	return nil
}

func ioNopCloser(body string) nopReadCloser {
	return nopReadCloser{Reader: strings.NewReader(body)}
}

func uintPtr(v uint) *uint {
	return &v
}
