package controller

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestBuildChannelBenchmarkWorkIncludesEveryConfiguredModel(t *testing.T) {
	t.Parallel()

	fallbackModel := "fallback-model"
	channels := []*model.Channel{
		{Id: 1, Type: constant.ChannelTypeOpenAI, Name: "primary", Models: "model-a,model-b"},
		{Id: 2, Type: constant.ChannelTypeAnthropic, Name: "fallback", TestModel: &fallbackModel},
	}

	work, results := buildChannelBenchmarkWork(channels)
	if len(work) != 2 || len(results) != 3 {
		t.Fatalf("work=%d results=%d, want 2 channel jobs and 3 model results", len(work), len(results))
	}
	if results[0].Model != "model-a" || results[1].Model != "model-b" || results[2].Model != fallbackModel {
		t.Fatalf("unexpected models: %#v", results)
	}
	for _, result := range results {
		if result.Status != "pending" {
			t.Fatalf("initial status = %q, want pending", result.Status)
		}
	}
}

func TestBuildChannelTestMetricsCalculatesTTFTAndTPS(t *testing.T) {
	t.Parallel()

	startedAt := time.Unix(100, 0)
	firstTokenAt := startedAt.Add(250 * time.Millisecond)
	finishedAt := firstTokenAt.Add(2 * time.Second)
	info := &relaycommon.RelayInfo{
		StartTime:         startedAt,
		FirstResponseTime: firstTokenAt,
		IsStream:          true,
	}

	metrics := buildChannelTestMetrics(info, &dto.Usage{CompletionTokens: 40}, finishedAt, 2250)
	if metrics.ttftMs == nil || *metrics.ttftMs != 250 {
		t.Fatalf("ttft = %v, want 250ms", metrics.ttftMs)
	}
	if metrics.tps == nil || *metrics.tps != 20 {
		t.Fatalf("tps = %v, want 20", metrics.tps)
	}
}

func TestApplyChannelTestOptionsSetsPromptAndThinking(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "old prompt"}},
	}

	err := applyChannelTestOptions(request, channelTestOptions{
		prompt:          "benchmark prompt",
		maxOutputTokens: 256,
		enableThinking:  true,
	})
	if err != nil {
		t.Fatalf("apply options: %v", err)
	}
	if request.Messages[0].Content != "benchmark prompt" {
		t.Fatalf("prompt = %#v, want benchmark prompt", request.Messages[0].Content)
	}
	if request.MaxTokens == nil || *request.MaxTokens != 256 {
		t.Fatalf("max tokens = %v, want 256", request.MaxTokens)
	}
	if request.ReasoningEffort != "high" {
		t.Fatalf("reasoning effort = %q, want high", request.ReasoningEffort)
	}
	if string(request.EnableThinking) != "true" {
		t.Fatalf("enable thinking = %s, want true", request.EnableThinking)
	}
}

func TestSelectChannelBenchmarkChannelsDefaultsToEnabled(t *testing.T) {
	channels := []*model.Channel{
		{Id: 1, Status: 1},
		{Id: 2, Status: 2},
		{Id: 3, Status: 1},
	}

	selected, selectedIDs := selectChannelBenchmarkChannels(channels, nil)
	if len(selected) != 2 || len(selectedIDs) != 2 || selectedIDs[0] != 1 || selectedIDs[1] != 3 {
		t.Fatalf("selected ids = %v, want [1 3]", selectedIDs)
	}
}
