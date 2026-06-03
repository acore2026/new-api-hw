package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	w3CredentialRefreshTickInterval = 10 * time.Minute
	w3CredentialRefreshThreshold    = 24 * time.Hour
	w3CredentialRefreshBatchSize    = 200
	w3CredentialRefreshTimeout      = 20 * time.Second
)

var (
	w3CredentialRefreshOnce    sync.Once
	w3CredentialRefreshRunning atomic.Bool
)

func StartW3CredentialAutoRefreshTask() {
	w3CredentialRefreshOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("w3 credential auto-refresh task started: tick=%s threshold=%s", w3CredentialRefreshTickInterval, w3CredentialRefreshThreshold))

			ticker := time.NewTicker(w3CredentialRefreshTickInterval)
			defer ticker.Stop()

			runW3CredentialAutoRefreshOnce()
			for range ticker.C {
				runW3CredentialAutoRefreshOnce()
			}
		})
	})
}

func runW3CredentialAutoRefreshOnce() {
	if !w3CredentialRefreshRunning.CompareAndSwap(false, true) {
		return
	}
	defer w3CredentialRefreshRunning.Store(false)

	ctx := context.Background()
	now := time.Now()

	var refreshed int
	var scanned int

	offset := 0
	for {
		var channels []*model.Channel
		err := model.DB.
			Select("id", "name", "key", "status", "channel_info", "settings").
			Where("type = ? AND (status = ? OR status = ?)",
				constant.ChannelTypeMiniMax,
				common.ChannelStatusEnabled,
				common.ChannelStatusAutoDisabled,
			).
			Order("id asc").
			Limit(w3CredentialRefreshBatchSize).
			Offset(offset).
			Find(&channels).Error
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("w3 credential auto-refresh: query channels failed: %v", err))
			return
		}
		if len(channels) == 0 {
			break
		}
		offset += w3CredentialRefreshBatchSize

		for _, ch := range channels {
			if ch == nil {
				continue
			}
			scanned++
			if !shouldAutoRefreshW3ChannelStatus(ch.Status) || ch.ChannelInfo.IsMultiKey {
				continue
			}
			if !ch.GetOtherSettings().W3OAuthEnabled {
				continue
			}

			rawKey := strings.TrimSpace(ch.Key)
			if rawKey == "" {
				continue
			}

			oauthKey, err := ParseW3OAuthKey(rawKey)
			if err != nil || strings.TrimSpace(oauthKey.RefreshToken) == "" {
				continue
			}
			if !W3OAuthKeyExpiresWithin(oauthKey, now, w3CredentialRefreshThreshold) {
				continue
			}

			refreshCtx, cancel := context.WithTimeout(ctx, w3CredentialRefreshTimeout)
			newKey, _, err := RefreshW3ChannelCredential(refreshCtx, ch.Id, W3CredentialRefreshOptions{ResetCaches: false, Force: true})
			cancel()
			if err != nil {
				logger.LogWarn(ctx, fmt.Sprintf("w3 credential auto-refresh: channel_id=%d name=%s refresh failed: %v", ch.Id, ch.Name, err))
				continue
			}

			refreshed++
			logger.LogInfo(ctx, fmt.Sprintf("w3 credential auto-refresh: channel_id=%d name=%s refreshed, expires_at=%s", ch.Id, ch.Name, newKey.Expired))
		}
	}

	if refreshed > 0 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.LogWarn(ctx, fmt.Sprintf("w3 credential auto-refresh: InitChannelCache panic: %v", r))
				}
			}()
			model.InitChannelCache()
		}()
		ResetProxyClientCache()
	}

	if common.DebugEnabled {
		logger.LogDebug(ctx, "w3 credential auto-refresh: scanned=%d refreshed=%d", scanned, refreshed)
	}
}
