package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
)

const (
	w3UserDetailPath       = "/auth/internal/getUserDetail"
	w3ModelsPath           = "/chat/modles?checkUserPermission=TRUE"
	w3AgentType            = "AgentCenter"
	w3PluginVersion        = "cli-1.2605.03-IN.1."
	w3AgentClientVersion   = "1.2605.03-IN.1"
	w3MaxModelsResponseLen = 4 << 20
)

type w3UserDetailResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Region         string `json:"region"`
		DepartmentPath string `json:"departmentPath"`
		W3Account      string `json:"w3account"`
	} `json:"data"`
}

type w3Model struct {
	Name    string `json:"name"`
	ModelID string `json:"modelId"`
}

// FetchW3Models resolves the account metadata required by CodeAgent before
// requesting the account's permitted model list.
func FetchW3Models(
	ctx context.Context,
	settings dto.ChannelOtherSettings,
	channelSettings dto.ChannelSettings,
	accessToken string,
) ([]string, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return nil, errors.New("w3 model fetch: access_token is required")
	}

	config := ResolveW3OAuthConfig(settings)
	if channelSettings.TLSInsecureSkipVerify {
		config.VerifyTLS = false
	}
	client, err := GetW3HTTPClient(channelSettings.Proxy, config.VerifyTLS, defaultW3HTTPTimeout)
	if err != nil {
		return nil, fmt.Errorf("w3 model fetch: create HTTP client: %w", err)
	}

	userDetail, err := fetchW3UserDetail(ctx, client, config, accessToken)
	if err != nil {
		return nil, err
	}

	modelsURL := config.APIBaseURL + w3ModelsPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("w3 model fetch: create models request: %w", err)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "axios/1.18.1")
	req.Header.Set("agent-type", w3AgentType)
	req.Header.Set("area", normalizeW3Area(userDetail.Data.Region))
	req.Header.Set("depart", userDetail.Data.DepartmentPath)
	req.Header.Set("plugin-version", w3PluginVersion)
	req.Header.Set("x-agent-client-version", w3AgentClientVersion)
	req.Header.Set("x-agent-user-account", userDetail.Data.W3Account)
	req.Header.Set("x-agent-user-department", userDetail.Data.DepartmentPath)
	req.Header.Set("x-auth-token", accessToken)

	logger.LogDebug(ctx, fmt.Sprintf(
		"w3 model fetch: url=%s account=%s department=%s area=%s",
		safeW3URL(modelsURL),
		userDetail.Data.W3Account,
		userDetail.Data.DepartmentPath,
		normalizeW3Area(userDetail.Data.Region),
	))

	var response []w3Model
	if err := doW3ModelJSONRequest(client, req, &response); err != nil {
		return nil, fmt.Errorf("w3 model fetch: %w", err)
	}

	modelIDs := make([]string, 0, len(response))
	seen := make(map[string]struct{}, len(response))
	for _, item := range response {
		modelID := strings.TrimSpace(item.ModelID)
		if modelID == "" {
			modelID = strings.TrimSpace(item.Name)
		}
		if modelID == "" {
			continue
		}
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}
		modelIDs = append(modelIDs, modelID)
	}
	if len(modelIDs) == 0 {
		return nil, errors.New("model list response contained no model IDs")
	}
	return modelIDs, nil
}

func fetchW3UserDetail(
	ctx context.Context,
	client *http.Client,
	config W3OAuthConfig,
	accessToken string,
) (*w3UserDetailResponse, error) {
	requestURL := config.APIBaseURL + w3UserDetailPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("w3 user detail: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "claude-proxy/1.1.1")
	req.Header.Set("X-Auth-Token", accessToken)
	req.Header.Set("X-Provider-ID", config.ProviderID)
	req.Header.Set("X-Request-ID", common.GetUUID())

	var response w3UserDetailResponse
	if err := doW3ModelJSONRequest(client, req, &response); err != nil {
		return nil, fmt.Errorf("w3 user detail: %w", err)
	}
	if response.Code != http.StatusOK {
		return nil, fmt.Errorf("upstream code %d: %s", response.Code, response.Message)
	}
	if strings.TrimSpace(response.Data.W3Account) == "" {
		return nil, errors.New("response missing w3account")
	}
	if strings.TrimSpace(response.Data.DepartmentPath) == "" {
		return nil, errors.New("response missing departmentPath")
	}
	return &response, nil
}

func doW3ModelJSONRequest(client *http.Client, req *http.Request, target any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, w3MaxModelsResponseLen+1))
	if err != nil {
		return err
	}
	if len(body) > w3MaxModelsResponseLen {
		return errors.New("upstream response exceeds size limit")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if len(message) > 1024 {
			message = message[:1024]
		}
		return fmt.Errorf("upstream status %d: %s", resp.StatusCode, message)
	}
	if err := common.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode upstream response: %w", err)
	}
	return nil
}

func normalizeW3Area(region string) string {
	switch strings.ToLower(strings.TrimSpace(region)) {
	case "y":
		return "yellow"
	case "b":
		return "blue"
	case "g":
		return "green"
	default:
		return strings.TrimSpace(region)
	}
}
