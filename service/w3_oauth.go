package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
)

const (
	DefaultW3APIBaseURL      = "https://codeagentcli.rnd.huawei.com/codeAgentPro"
	DefaultW3AuthURL         = "https://ssoproxysvr.cd-cloud-ssoproxysvr.szv.dragon.tools.huawei.com/ssoproxysvr/oauth2/authorize"
	DefaultW3ClientID        = "com.huawei.devmind.codebot.apibot"
	DefaultW3Scope           = "1000:1002"
	DefaultW3ProviderID      = "hw-minimax"
	defaultW3HTTPTimeout     = 30 * time.Second
	defaultW3RefreshLifetime = 70 * time.Hour
)

var ErrW3OAuthTokenPending = errors.New("w3 oauth token pending")

type W3OAuthConfig struct {
	APIBaseURL      string
	AuthURL         string
	TokenURL        string
	RefreshURL      string
	ClientID        string
	CallbackURLBase string
	Scope           string
	ProviderID      string
	VerifyTLS       bool
}

type W3OAuthAuthorizationFlow struct {
	ClientCode   string
	AuthorizeURL string
	Config       W3OAuthConfig
}

type W3OAuthTokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

type w3OAuthExpiresIn struct {
	Value int
}

func (e *w3OAuthExpiresIn) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		e.Value = 0
		return nil
	}
	if strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") {
		raw = strings.Trim(raw, "\"")
	}
	if strings.TrimSpace(raw) == "" {
		e.Value = 0
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("invalid expires_in value %q: %w", raw, err)
	}
	e.Value = value
	return nil
}

type w3OAuthTokenPayload struct {
	AccessToken       string               `json:"access_token"`
	AccessTokenCamel  string               `json:"accessToken"`
	RefreshToken      string               `json:"refresh_token"`
	RefreshTokenCamel string               `json:"refreshToken"`
	ExpiresIn         w3OAuthExpiresIn     `json:"expires_in"`
	ExpiresInCamel    w3OAuthExpiresIn     `json:"expiresIn"`
	Data              *w3OAuthTokenPayload `json:"data"`
	Result            *w3OAuthTokenPayload `json:"result"`
}

type W3OAuthKey struct {
	Type         string `json:"type,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Expired      string `json:"expired,omitempty"`
	LastRefresh  string `json:"last_refresh,omitempty"`
}

func ResolveW3OAuthConfig(settings dto.ChannelOtherSettings) W3OAuthConfig {
	apiBaseURL := strings.TrimRight(strings.TrimSpace(settings.W3ApiBaseURL), "/")
	if apiBaseURL == "" {
		apiBaseURL = DefaultW3APIBaseURL
	}

	authURL := strings.TrimSpace(settings.W3AuthURL)
	if authURL == "" {
		authURL = DefaultW3AuthURL
	}
	tokenURL := strings.TrimSpace(settings.W3TokenURL)
	if tokenURL == "" {
		tokenURL = apiBaseURL + "/oauth/getToken"
	}
	refreshURL := strings.TrimSpace(settings.W3RefreshURL)
	if refreshURL == "" {
		refreshURL = apiBaseURL + "/oauth/refreshToken"
	}
	clientID := strings.TrimSpace(settings.W3ClientID)
	if clientID == "" {
		clientID = DefaultW3ClientID
	}
	callbackURLBase := strings.TrimSpace(settings.W3CallbackURLBase)
	if callbackURLBase == "" {
		callbackURLBase = apiBaseURL + "/oauth/callback"
	}
	scope := strings.TrimSpace(settings.W3Scope)
	if scope == "" {
		scope = DefaultW3Scope
	}
	providerID := strings.TrimSpace(settings.W3ProviderID)
	if providerID == "" {
		providerID = DefaultW3ProviderID
	}

	return W3OAuthConfig{
		APIBaseURL:      apiBaseURL,
		AuthURL:         authURL,
		TokenURL:        tokenURL,
		RefreshURL:      refreshURL,
		ClientID:        clientID,
		CallbackURLBase: callbackURLBase,
		Scope:           scope,
		ProviderID:      providerID,
		VerifyTLS:       settings.W3VerifyTLS,
	}
}

func ParseW3OAuthKey(raw string) (*W3OAuthKey, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("w3 channel: empty oauth key")
	}
	var key W3OAuthKey
	if err := common.Unmarshal([]byte(raw), &key); err != nil {
		return nil, errors.New("w3 channel: invalid oauth key json")
	}
	if strings.TrimSpace(key.Type) != "" && strings.TrimSpace(key.Type) != "w3" {
		return nil, errors.New("w3 channel: credential type is not w3")
	}
	if strings.TrimSpace(key.AccessToken) == "" {
		return nil, errors.New("w3 channel: access_token is required")
	}
	return &key, nil
}

func EncodeW3OAuthKey(key *W3OAuthKey) (string, error) {
	if key == nil {
		return "", errors.New("w3 channel: nil oauth key")
	}
	if strings.TrimSpace(key.Type) == "" {
		key.Type = "w3"
	}
	encoded, err := common.Marshal(key)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func CreateW3OAuthAuthorizationFlow(config W3OAuthConfig) (*W3OAuthAuthorizationFlow, error) {
	clientCode, err := generateW3ClientCode()
	if err != nil {
		return nil, err
	}
	authorizeURL, err := BuildW3AuthorizeURL(config, clientCode)
	if err != nil {
		return nil, err
	}
	return &W3OAuthAuthorizationFlow{
		ClientCode:   clientCode,
		AuthorizeURL: authorizeURL,
		Config:       config,
	}, nil
}

func BuildW3AuthorizeURL(config W3OAuthConfig, clientCode string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(config.AuthURL))
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("client_id", strings.TrimSpace(config.ClientID))
	q.Set("redirect_uri", fmt.Sprintf("%s?client_code=%s", strings.TrimRight(config.CallbackURLBase, "?"), clientCode))
	q.Set("scope", strings.TrimSpace(config.Scope))
	q.Set("response_type", "code")
	q.Set("scope_resource", "devuc")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func PollW3OAuthToken(ctx context.Context, config W3OAuthConfig, proxyURL string, clientCode string) (*W3OAuthTokenResult, error) {
	clientCode = strings.TrimSpace(clientCode)
	if clientCode == "" {
		return nil, errors.New("empty client_code")
	}
	callbackURL := fmt.Sprintf("%s?client_code=%s", strings.TrimRight(config.CallbackURLBase, "?"), clientCode)
	payload := map[string]string{
		"clientCode":  clientCode,
		"redirectUrl": callbackURL,
	}

	var lastErr error
	attempt := 0
	logger.LogDebug(ctx, fmt.Sprintf("w3 oauth token poll start: token_url=%s callback_url=%s client_code=%s proxy=disabled", safeW3URL(config.TokenURL), safeW3URL(callbackURL), shortW3Value(clientCode)))
	for {
		attempt++
		res, err := requestW3Token(ctx, config, proxyURL, http.MethodPost, config.TokenURL, nil, payload)
		if err == nil {
			logger.LogDebug(ctx, fmt.Sprintf("w3 oauth token poll success: token_url=%s client_code=%s attempt=%d expires_at=%s", safeW3URL(config.TokenURL), shortW3Value(clientCode), attempt, res.ExpiresAt.Format(time.RFC3339)))
			return res, nil
		}
		if !IsW3OAuthTokenPending(err) {
			logger.LogWarn(ctx, fmt.Sprintf("w3 oauth token poll failed: token_url=%s client_code=%s attempt=%d error=%v", safeW3URL(config.TokenURL), shortW3Value(clientCode), attempt, err))
			return nil, err
		}
		lastErr = err
		logger.LogDebug(ctx, fmt.Sprintf("w3 oauth token pending: token_url=%s client_code=%s attempt=%d", safeW3URL(config.TokenURL), shortW3Value(clientCode), attempt))

		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastErr != nil {
				return nil, fmt.Errorf("%w: %w", ctx.Err(), lastErr)
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func IsW3OAuthTokenPending(err error) bool {
	return errors.Is(err, ErrW3OAuthTokenPending)
}

func RefreshW3OAuthToken(ctx context.Context, config W3OAuthConfig, proxyURL string, refreshToken string) (*W3OAuthTokenResult, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, errors.New("empty refresh_token")
	}
	logger.LogDebug(ctx, fmt.Sprintf("w3 oauth token refresh start: refresh_url=%s proxy=disabled", safeW3URL(config.RefreshURL)))
	headers := map[string]string{"x-refresh-token": refreshToken}
	res, err := requestW3Token(ctx, config, proxyURL, http.MethodPost, config.RefreshURL, headers, nil)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("w3 oauth token refresh failed: refresh_url=%s error=%v", safeW3URL(config.RefreshURL), err))
		return nil, err
	}
	logger.LogDebug(ctx, fmt.Sprintf("w3 oauth token refresh success: refresh_url=%s expires_at=%s", safeW3URL(config.RefreshURL), res.ExpiresAt.Format(time.RFC3339)))
	return res, nil
}

func GetW3HTTPClient(proxyURL string, verifyTLS bool, timeout time.Duration) (*http.Client, error) {
	if strings.TrimSpace(proxyURL) != "" {
		logger.LogDebug(context.Background(), "w3 http client: ignoring configured proxy because W3 endpoints are forced direct")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns = common.RelayMaxIdleConns
	transport.MaxIdleConnsPerHost = common.RelayMaxIdleConnsPerHost
	transport.ForceAttemptHTTP2 = true
	if !verifyTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	client := &http.Client{
		Transport:     transport,
		CheckRedirect: checkRedirect,
	}
	if timeout > 0 {
		client.Timeout = timeout
	} else if timeout == 0 {
		client.Timeout = defaultW3HTTPTimeout
	}
	return client, nil
}

func requestW3Token(
	ctx context.Context,
	config W3OAuthConfig,
	proxyURL string,
	method string,
	requestURL string,
	headers map[string]string,
	payload map[string]string,
) (*W3OAuthTokenResult, error) {
	client, err := GetW3HTTPClient(proxyURL, config.VerifyTLS, defaultW3HTTPTimeout)
	if err != nil {
		return nil, err
	}

	var body io.Reader
	if payload != nil {
		encoded, err := common.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	operation := "refresh"
	if payload != nil {
		operation = "login-poll"
	}
	logger.LogDebug(ctx, fmt.Sprintf("w3 oauth request: operation=%s method=%s url=%s verify_tls=%t proxy=disabled", operation, method, safeW3URL(requestURL), config.VerifyTLS))

	resp, err := client.Do(req)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("w3 oauth request transport failed: operation=%s url=%s error=%v", operation, safeW3URL(requestURL), err))
		return nil, err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("w3 oauth response read failed: operation=%s url=%s status=%d error=%v", operation, safeW3URL(requestURL), resp.StatusCode, err))
		return nil, err
	}
	logger.LogDebug(ctx, fmt.Sprintf("w3 oauth response: operation=%s url=%s status=%d bytes=%d", operation, safeW3URL(requestURL), resp.StatusCode, len(responseBody)))
	var tokenPayload w3OAuthTokenPayload
	if len(responseBody) > 0 {
		if err := common.Unmarshal(responseBody, &tokenPayload); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("w3 oauth response invalid json: operation=%s url=%s status=%d bytes=%d error=%v", operation, safeW3URL(requestURL), resp.StatusCode, len(responseBody), err))
			return nil, fmt.Errorf("w3 oauth token response is not valid json: %w", err)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logger.LogWarn(ctx, fmt.Sprintf("w3 oauth response non-2xx: operation=%s url=%s status=%d bytes=%d", operation, safeW3URL(requestURL), resp.StatusCode, len(responseBody)))
		return nil, fmt.Errorf("w3 oauth token request failed: status=%d", resp.StatusCode)
	}
	accessToken, refreshToken, expiresIn := normalizeW3OAuthTokenPayload(&tokenPayload)
	if strings.TrimSpace(accessToken) == "" {
		if payload != nil {
			logger.LogDebug(ctx, fmt.Sprintf("w3 oauth login token not ready: url=%s status=%d bytes=%d", safeW3URL(requestURL), resp.StatusCode, len(responseBody)))
			return nil, ErrW3OAuthTokenPending
		}
		logger.LogWarn(ctx, fmt.Sprintf("w3 oauth refresh response missing access_token: url=%s status=%d bytes=%d", safeW3URL(requestURL), resp.StatusCode, len(responseBody)))
		return nil, errors.New("w3 oauth token response missing access_token")
	}
	if strings.TrimSpace(refreshToken) == "" {
		refreshToken = strings.TrimSpace(headers["x-refresh-token"])
	}
	if strings.TrimSpace(refreshToken) == "" {
		logger.LogWarn(ctx, fmt.Sprintf("w3 oauth response missing refresh_token: operation=%s url=%s status=%d bytes=%d", operation, safeW3URL(requestURL), resp.StatusCode, len(responseBody)))
		return nil, errors.New("w3 oauth token response missing refresh_token")
	}

	expiresAt := time.Now().Add(defaultW3RefreshLifetime)
	if expiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	}
	return &W3OAuthTokenResult{
		AccessToken:  strings.TrimSpace(accessToken),
		RefreshToken: strings.TrimSpace(refreshToken),
		ExpiresAt:    expiresAt,
	}, nil
}

func normalizeW3OAuthTokenPayload(payload *w3OAuthTokenPayload) (accessToken string, refreshToken string, expiresIn int) {
	if payload == nil {
		return "", "", 0
	}
	if payload.Data != nil {
		if accessToken, refreshToken, expiresIn = normalizeW3OAuthTokenPayload(payload.Data); strings.TrimSpace(accessToken) != "" {
			return accessToken, refreshToken, expiresIn
		}
	}
	if payload.Result != nil {
		if accessToken, refreshToken, expiresIn = normalizeW3OAuthTokenPayload(payload.Result); strings.TrimSpace(accessToken) != "" {
			return accessToken, refreshToken, expiresIn
		}
	}
	accessToken = payload.AccessToken
	if strings.TrimSpace(accessToken) == "" {
		accessToken = payload.AccessTokenCamel
	}
	refreshToken = payload.RefreshToken
	if strings.TrimSpace(refreshToken) == "" {
		refreshToken = payload.RefreshTokenCamel
	}
	expiresIn = payload.ExpiresIn.Value
	if expiresIn <= 0 {
		expiresIn = payload.ExpiresInCamel.Value
	}
	return accessToken, refreshToken, expiresIn
}

func safeW3URL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return strings.TrimSpace(rawURL)
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func shortW3Value(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		return value
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func generateW3ClientCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
