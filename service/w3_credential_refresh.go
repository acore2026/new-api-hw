package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

type W3CredentialRefreshOptions struct {
	ResetCaches bool
	Force       bool
}

const W3CredentialRefreshThreshold = 5 * time.Minute

func W3OAuthKeyExpiresWithin(key *W3OAuthKey, now time.Time, threshold time.Duration) bool {
	if key == nil {
		return true
	}
	expiredAtRaw := strings.TrimSpace(key.Expired)
	if expiredAtRaw == "" {
		return true
	}
	expiredAt, err := time.Parse(time.RFC3339, expiredAtRaw)
	if err != nil || expiredAt.IsZero() {
		return true
	}
	return expiredAt.Sub(now) <= threshold
}

func RefreshW3ChannelCredential(ctx context.Context, channelID int, opts W3CredentialRefreshOptions) (*W3OAuthKey, *model.Channel, error) {
	ch, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, nil, err
	}
	if ch == nil {
		return nil, nil, errors.New("channel not found")
	}
	if ch.Type != constant.ChannelTypeMiniMax {
		return nil, nil, errors.New("channel type is not MiniMax")
	}
	settings := ch.GetOtherSettings()
	if !settings.W3OAuthEnabled {
		return nil, nil, errors.New("channel W3 OAuth is not enabled")
	}
	if ch.ChannelInfo.IsMultiKey {
		return nil, nil, errors.New("W3 OAuth refresh does not support multi-key channels")
	}

	oauthKey, err := ParseW3OAuthKey(strings.TrimSpace(ch.Key))
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(oauthKey.RefreshToken) == "" {
		return nil, nil, errors.New("w3 channel: refresh_token is required to refresh credential")
	}
	if !opts.Force && !W3OAuthKeyExpiresWithin(oauthKey, time.Now(), W3CredentialRefreshThreshold) {
		return oauthKey, ch, nil
	}

	refreshCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	config := ResolveW3OAuthConfig(settings)
	channelSetting := ch.GetSetting()
	if channelSetting.TLSInsecureSkipVerify {
		config.VerifyTLS = false
	}
	res, err := RefreshW3OAuthToken(refreshCtx, config, channelSetting.Proxy, oauthKey.RefreshToken)
	if err != nil {
		return nil, nil, err
	}

	oauthKey.Type = "w3"
	oauthKey.AccessToken = res.AccessToken
	oauthKey.RefreshToken = res.RefreshToken
	oauthKey.LastRefresh = time.Now().Format(time.RFC3339)
	oauthKey.Expired = res.ExpiresAt.Format(time.RFC3339)

	encoded, err := EncodeW3OAuthKey(oauthKey)
	if err != nil {
		return nil, nil, err
	}

	if err := model.DB.Model(&model.Channel{}).Where("id = ?", ch.Id).Update("key", encoded).Error; err != nil {
		return nil, nil, fmt.Errorf("update channel key failed: %w", err)
	}

	if opts.ResetCaches {
		model.InitChannelCache()
		ResetProxyClientCache()
	}

	return oauthKey, ch, nil
}

func SaveW3ChannelCredential(channelID int, key *W3OAuthKey, resetCaches bool) error {
	encoded, err := EncodeW3OAuthKey(key)
	if err != nil {
		return err
	}
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("key", encoded).Error; err != nil {
		return err
	}
	if resetCaches {
		model.InitChannelCache()
		ResetProxyClientCache()
	}
	return nil
}

func shouldAutoRefreshW3ChannelStatus(status int) bool {
	return status == common.ChannelStatusEnabled || status == common.ChannelStatusAutoDisabled
}
