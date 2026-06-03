package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

type w3OAuthStartRequest struct {
	Proxy             string `json:"proxy"`
	W3ProviderID      string `json:"w3_provider_id"`
	W3VerifyTLS       bool   `json:"w3_verify_tls"`
	W3ApiBaseURL      string `json:"w3_api_base_url"`
	W3AuthURL         string `json:"w3_auth_url"`
	W3TokenURL        string `json:"w3_token_url"`
	W3RefreshURL      string `json:"w3_refresh_url"`
	W3ClientID        string `json:"w3_client_id"`
	W3CallbackURLBase string `json:"w3_callback_url_base"`
	W3Scope           string `json:"w3_scope"`
}

func w3OAuthSessionKey(channelID int, field string) string {
	return fmt.Sprintf("w3_oauth_%s_%d", field, channelID)
}

func shortW3ControllerValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		return value
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func StartW3OAuth(c *gin.Context) {
	startW3OAuthWithChannelID(c, 0)
}

func StartW3OAuthForChannel(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}
	startW3OAuthWithChannelID(c, channelID)
}

func startW3OAuthWithChannelID(c *gin.Context, channelID int) {
	settings, proxy, err := resolveW3OAuthStartSettings(c, channelID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	config := service.ResolveW3OAuthConfig(settings)
	flow, err := service.CreateW3OAuthAuthorizationFlow(config)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	configJSON, err := common.Marshal(config)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	session := sessions.Default(c)
	session.Set(w3OAuthSessionKey(channelID, "client_code"), flow.ClientCode)
	session.Set(w3OAuthSessionKey(channelID, "config"), string(configJSON))
	session.Set(w3OAuthSessionKey(channelID, "proxy"), proxy)
	session.Set(w3OAuthSessionKey(channelID, "created_at"), time.Now().Unix())
	_ = session.Save()

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("w3 oauth start: channel_id=%d client_code=%s auth_url=%s token_url=%s callback_base=%s proxy=disabled", channelID, shortW3ControllerValue(flow.ClientCode), config.AuthURL, config.TokenURL, config.CallbackURLBase))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"authorize_url": flow.AuthorizeURL,
			"client_code":   flow.ClientCode,
		},
	})
}

func CompleteW3OAuth(c *gin.Context) {
	completeW3OAuthWithChannelID(c, 0)
}

func CompleteW3OAuthForChannel(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}
	completeW3OAuthWithChannelID(c, channelID)
}

func completeW3OAuthWithChannelID(c *gin.Context, channelID int) {
	if channelID > 0 {
		if err := validateSavedW3Channel(channelID); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
	}

	session := sessions.Default(c)
	clientCode, _ := session.Get(w3OAuthSessionKey(channelID, "client_code")).(string)
	configRaw, _ := session.Get(w3OAuthSessionKey(channelID, "config")).(string)
	proxy, _ := session.Get(w3OAuthSessionKey(channelID, "proxy")).(string)
	if strings.TrimSpace(clientCode) == "" || strings.TrimSpace(configRaw) == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "oauth flow not started or session expired"})
		return
	}

	var config service.W3OAuthConfig
	if err := common.Unmarshal([]byte(configRaw), &config); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "oauth session config is invalid"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	logger.LogDebug(c.Request.Context(), fmt.Sprintf("w3 oauth complete attempt: channel_id=%d client_code=%s token_url=%s proxy=disabled", channelID, shortW3ControllerValue(clientCode), config.TokenURL))

	tokenRes, err := service.PollW3OAuthToken(ctx, config, proxy, clientCode)
	if err != nil {
		if !service.IsW3OAuthTokenPending(err) {
			common.SysError("failed to complete w3 oauth: " + err.Error())
		}
		logger.LogDebug(c.Request.Context(), fmt.Sprintf("w3 oauth complete pending: channel_id=%d client_code=%s token_url=%s error=%v", channelID, shortW3ControllerValue(clientCode), config.TokenURL, err))
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "W3 OAuth token is not ready, please retry after authorization",
			"data": gin.H{
				"pending": true,
			},
		})
		return
	}

	key := &service.W3OAuthKey{
		Type:         "w3",
		AccessToken:  tokenRes.AccessToken,
		RefreshToken: tokenRes.RefreshToken,
		Expired:      tokenRes.ExpiresAt.Format(time.RFC3339),
		LastRefresh:  time.Now().Format(time.RFC3339),
	}
	encoded, err := service.EncodeW3OAuthKey(key)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	session.Delete(w3OAuthSessionKey(channelID, "client_code"))
	session.Delete(w3OAuthSessionKey(channelID, "config"))
	session.Delete(w3OAuthSessionKey(channelID, "proxy"))
	session.Delete(w3OAuthSessionKey(channelID, "created_at"))
	_ = session.Save()

	if channelID > 0 {
		if err := service.SaveW3ChannelCredential(channelID, key, true); err != nil {
			common.ApiError(c, err)
			return
		}
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("w3 oauth complete saved: channel_id=%d expires_at=%s", channelID, key.Expired))
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "saved",
			"data": gin.H{
				"channel_id":   channelID,
				"expires_at":   key.Expired,
				"last_refresh": key.LastRefresh,
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "generated",
		"data": gin.H{
			"key":          encoded,
			"expires_at":   key.Expired,
			"last_refresh": key.LastRefresh,
		},
	})
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("w3 oauth complete generated: channel_id=%d expires_at=%s", channelID, key.Expired))
}

func RefreshW3ChannelCredential(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	oauthKey, ch, err := service.RefreshW3ChannelCredential(ctx, channelID, service.W3CredentialRefreshOptions{ResetCaches: true, Force: true})
	if err != nil {
		common.SysError("failed to refresh w3 channel credential: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "刷新凭证失败，请稍后重试"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "refreshed",
		"data": gin.H{
			"expires_at":   oauthKey.Expired,
			"last_refresh": oauthKey.LastRefresh,
			"channel_id":   ch.Id,
			"channel_type": ch.Type,
			"channel_name": ch.Name,
		},
	})
}

func resolveW3OAuthStartSettings(c *gin.Context, channelID int) (dto.ChannelOtherSettings, string, error) {
	if channelID > 0 {
		ch, err := model.GetChannelById(channelID, false)
		if err != nil {
			return dto.ChannelOtherSettings{}, "", err
		}
		if ch == nil {
			return dto.ChannelOtherSettings{}, "", errors.New("channel not found")
		}
		if ch.Type != constant.ChannelTypeMiniMax {
			return dto.ChannelOtherSettings{}, "", errors.New("channel type is not MiniMax")
		}
		settings := ch.GetOtherSettings()
		if !settings.W3OAuthEnabled {
			return dto.ChannelOtherSettings{}, "", errors.New("channel W3 OAuth is not enabled")
		}
		return settings, ch.GetSetting().Proxy, nil
	}

	var req w3OAuthStartRequest
	if c.Request != nil && c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			return dto.ChannelOtherSettings{}, "", err
		}
	}
	settings := dto.ChannelOtherSettings{
		W3OAuthEnabled:    true,
		W3ProviderID:      req.W3ProviderID,
		W3VerifyTLS:       req.W3VerifyTLS,
		W3ApiBaseURL:      req.W3ApiBaseURL,
		W3AuthURL:         req.W3AuthURL,
		W3TokenURL:        req.W3TokenURL,
		W3RefreshURL:      req.W3RefreshURL,
		W3ClientID:        req.W3ClientID,
		W3CallbackURLBase: req.W3CallbackURLBase,
		W3Scope:           req.W3Scope,
	}
	return settings, strings.TrimSpace(req.Proxy), nil
}

func validateSavedW3Channel(channelID int) error {
	ch, err := model.GetChannelById(channelID, false)
	if err != nil {
		return err
	}
	if ch == nil {
		return errors.New("channel not found")
	}
	if ch.Type != constant.ChannelTypeMiniMax {
		return errors.New("channel type is not MiniMax")
	}
	if !ch.GetOtherSettings().W3OAuthEnabled {
		return errors.New("channel W3 OAuth is not enabled")
	}
	return nil
}
