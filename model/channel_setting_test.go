package model

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestChannelSettingPreservesTLSInsecureSkipVerify(t *testing.T) {
	channel := &Channel{}
	channel.SetSetting(dto.ChannelSettings{
		Proxy:                 "socks5://127.0.0.1:1080",
		TLSInsecureSkipVerify: true,
	})

	require.NotNil(t, channel.Setting)
	setting := channel.GetSetting()
	require.Equal(t, "socks5://127.0.0.1:1080", setting.Proxy)
	require.True(t, setting.TLSInsecureSkipVerify)
}
