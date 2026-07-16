package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	channelconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
)

func TestConvertOpenAIRequestAppliesMiniMaxCompatibility(t *testing.T) {
	t.Parallel()

	temperature := 0.7
	request := &dto.GeneralOpenAIRequest{
		Model:       "MiniMax-M2.7",
		MaxTokens:   common.GetPointer(uint(30000)),
		Temperature: &temperature,
		Messages: []dto.Message{
			{Role: "system", Content: "x-anthropic-billing-header: cc_version=2.1.0;You are Claude Code"},
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: channelconstant.ChannelTypeOpenAI,
			ChannelSetting: dto.ChannelSettings{
				MiniMaxCompatibilityEnabled: true,
			},
		},
	}

	got, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	converted := got.(*dto.GeneralOpenAIRequest)
	if converted.Temperature != nil {
		t.Fatalf("temperature = %#v, want nil", converted.Temperature)
	}
	if converted.MaxTokens == nil || *converted.MaxTokens != 24576 {
		t.Fatalf("max_tokens = %#v, want 24576", converted.MaxTokens)
	}
	if content := converted.Messages[0].StringContent(); content != "You are Claude Code" {
		t.Fatalf("system content = %q, want Claude Code prompt without billing prefix", content)
	}
	if gotFormat := info.GetFinalRequestRelayFormat(); gotFormat != types.RelayFormatOpenAI {
		t.Fatalf("final request relay format = %q, want %q", gotFormat, types.RelayFormatOpenAI)
	}
}
