package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
)

func TestFetchW3ModelsUsesUserDetailHeaders(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case w3UserDetailPath:
			if r.Method != http.MethodPost || r.Header.Get("X-Auth-Token") != "wire-token" {
				t.Fatalf("unexpected user-detail request: method=%s token=%q", r.Method, r.Header.Get("X-Auth-Token"))
			}
			_, _ = w.Write([]byte(`{"code":200,"message":"OK","data":{"region":"y","departmentPath":"075774/031562","w3account":"l00852386"}}`))
		case "/chat/modles":
			if r.URL.Query().Get("checkUserPermission") != "TRUE" {
				t.Fatalf("checkUserPermission = %q", r.URL.Query().Get("checkUserPermission"))
			}
			if r.Header.Get("agent-type") != w3AgentType ||
				r.Header.Get("area") != "yellow" ||
				r.Header.Get("depart") != "075774/031562" ||
				r.Header.Get("x-agent-user-account") != "l00852386" ||
				r.Header.Get("x-agent-user-department") != "075774/031562" {
				t.Fatalf("unexpected model request headers: %v", r.Header)
			}
			_, _ = w.Write([]byte(`[
				{"name":"MiniMax-M2.7","modelId":"MiniMax-M2.7"},
				{
					"name":"GLM Auto",
					"modelId":"GLM-5.1-CodeAgent-Auto",
					"routModels":[
						{"name":"GLM-5.1-CodeAgent","modelId":"GLM-5.1-CodeAgent"},
						{"name":"Qwen3.6-27B-VL","modelId":"Qwen3.6-27B-VL"}
					]
				}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	models, err := FetchW3Models(
		context.Background(),
		dto.ChannelOtherSettings{
			W3OAuthEnabled: true,
			W3ApiBaseURL:   server.URL,
		},
		dto.ChannelSettings{},
		"wire-token",
	)
	if err != nil {
		t.Fatalf("FetchW3Models returned error: %v", err)
	}
	if len(models) != 3 ||
		models[0] != "MiniMax-M2.7" ||
		models[1] != "GLM-5.1-CodeAgent" ||
		models[2] != "Qwen3.6-27B-VL" {
		t.Fatalf("models = %#v", models)
	}
}
