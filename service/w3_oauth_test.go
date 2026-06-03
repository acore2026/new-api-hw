package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
)

func TestBuildW3AuthorizeURLContainsOAuthParameters(t *testing.T) {
	t.Parallel()

	config := ResolveW3OAuthConfig(dto.ChannelOtherSettings{
		W3AuthURL:         "https://login.example.com/oauth2/authorize",
		W3ClientID:        "client-id",
		W3CallbackURLBase: "https://api.example.com/oauth/callback",
		W3Scope:           "1000:1002",
	})

	got, err := BuildW3AuthorizeURL(config, "abc123")
	if err != nil {
		t.Fatalf("BuildW3AuthorizeURL returned error: %v", err)
	}

	for _, want := range []string{
		"client_id=client-id",
		"response_type=code",
		"scope=1000%3A1002",
		"scope_resource=devuc",
		"client_code%3Dabc123",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("authorize URL %q does not contain %q", got, want)
		}
	}
}

func TestPollW3OAuthTokenStoresTokenFields(t *testing.T) {
	t.Parallel()

	var gotPayload map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/getToken" {
			t.Fatalf("path = %s, want /oauth/getToken", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("Decode request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access",
			"refresh_token": "refresh",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	config := ResolveW3OAuthConfig(dto.ChannelOtherSettings{
		W3ApiBaseURL:      server.URL,
		W3TokenURL:        server.URL + "/oauth/getToken",
		W3CallbackURLBase: server.URL + "/oauth/callback",
	})

	res, err := PollW3OAuthToken(context.Background(), config, "", "client-code")
	if err != nil {
		t.Fatalf("PollW3OAuthToken returned error: %v", err)
	}
	if res.AccessToken != "access" || res.RefreshToken != "refresh" || res.ExpiresAt.IsZero() {
		t.Fatalf("token result = %+v, want access/refresh/expires", res)
	}
	if gotPayload["clientCode"] != "client-code" {
		t.Fatalf("clientCode = %q, want client-code", gotPayload["clientCode"])
	}
	if !strings.Contains(gotPayload["redirectUrl"], "client_code=client-code") {
		t.Fatalf("redirectUrl = %q, want client_code", gotPayload["redirectUrl"])
	}
}

func TestRefreshW3OAuthTokenSendsRefreshHeader(t *testing.T) {
	t.Parallel()

	var gotRefresh string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/refreshToken" {
			t.Fatalf("path = %s, want /oauth/refreshToken", r.URL.Path)
		}
		gotRefresh = r.Header.Get("x-refresh-token")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-access",
		})
	}))
	defer server.Close()

	config := ResolveW3OAuthConfig(dto.ChannelOtherSettings{
		W3RefreshURL: server.URL + "/oauth/refreshToken",
	})

	res, err := RefreshW3OAuthToken(context.Background(), config, "", "refresh-token")
	if err != nil {
		t.Fatalf("RefreshW3OAuthToken returned error: %v", err)
	}
	if gotRefresh != "refresh-token" {
		t.Fatalf("x-refresh-token = %q, want refresh-token", gotRefresh)
	}
	if res.AccessToken != "new-access" || res.RefreshToken != "refresh-token" {
		t.Fatalf("token result = %+v, want refreshed access and preserved refresh", res)
	}
}

func TestGetW3HTTPClientIgnoresConfiguredProxy(t *testing.T) {
	t.Parallel()

	client, err := GetW3HTTPClient("not-a-valid-proxy-url", false, 0)
	if err != nil {
		t.Fatalf("GetW3HTTPClient returned error: %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatalf("transport.Proxy is set, want nil direct transport")
	}
}

func TestGetW3HTTPClientAllowsNoTotalTimeout(t *testing.T) {
	t.Parallel()

	client, err := GetW3HTTPClient("", false, -1)
	if err != nil {
		t.Fatalf("GetW3HTTPClient returned error: %v", err)
	}
	if client.Timeout != 0 {
		t.Fatalf("client.Timeout = %s, want no total timeout", client.Timeout)
	}
}

func TestRequestW3TokenReadsWrappedTokenPayload(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"access_token":  "access",
				"refresh_token": "refresh",
				"expires_in":    3600,
			},
		})
	}))
	defer server.Close()

	config := ResolveW3OAuthConfig(dto.ChannelOtherSettings{})
	res, err := requestW3Token(context.Background(), config, "", http.MethodPost, server.URL, nil, map[string]string{
		"clientCode": "client-code",
	})
	if err != nil {
		t.Fatalf("requestW3Token returned error: %v", err)
	}
	if res.AccessToken != "access" || res.RefreshToken != "refresh" || res.ExpiresAt.IsZero() {
		t.Fatalf("token result = %+v, want wrapped access/refresh/expires", res)
	}
}

func TestRequestW3TokenAcceptsStringExpiresIn(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access",
			"refresh_token": "refresh",
			"scope":         "1000:1002",
			"token_type":    "Bearer",
			"expires_in":    "259199",
			"userAccount":   "user",
		})
	}))
	defer server.Close()

	config := ResolveW3OAuthConfig(dto.ChannelOtherSettings{})
	res, err := requestW3Token(context.Background(), config, "", http.MethodPost, server.URL, nil, map[string]string{
		"clientCode": "client-code",
	})
	if err != nil {
		t.Fatalf("requestW3Token returned error: %v", err)
	}
	if res.AccessToken != "access" || res.RefreshToken != "refresh" || res.ExpiresAt.IsZero() {
		t.Fatalf("token result = %+v, want access/refresh/expires", res)
	}
}

func TestRequestW3TokenMissingAccessTokenIsPendingForLoginPoll(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": "not ready",
		})
	}))
	defer server.Close()

	config := ResolveW3OAuthConfig(dto.ChannelOtherSettings{})
	_, err := requestW3Token(context.Background(), config, "", http.MethodPost, server.URL, nil, map[string]string{
		"clientCode": "client-code",
	})
	if !errors.Is(err, ErrW3OAuthTokenPending) {
		t.Fatalf("requestW3Token error = %v, want ErrW3OAuthTokenPending", err)
	}
}
