package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewRelayHTTPClientTLSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	secureClient, err := newRelayHTTPClient("", false)
	require.NoError(t, err)
	secureResponse, secureErr := secureClient.Get(server.URL)
	if secureResponse != nil {
		secureResponse.Body.Close()
	}
	require.Error(t, secureErr)

	insecureClient, err := newRelayHTTPClient("", true)
	require.NoError(t, err)
	insecureResponse, err := insecureClient.Get(server.URL)
	require.NoError(t, err)
	defer insecureResponse.Body.Close()
	require.Equal(t, http.StatusNoContent, insecureResponse.StatusCode)

	secureTransport, ok := secureClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.True(t, secureTransport.TLSClientConfig == nil || !secureTransport.TLSClientConfig.InsecureSkipVerify)

	insecureTransport, ok := insecureClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, insecureTransport.TLSClientConfig)
	require.True(t, insecureTransport.TLSClientConfig.InsecureSkipVerify)

	insecureDialer, err := GetWebsocketDialerWithOptions("", true)
	require.NoError(t, err)
	require.NotNil(t, insecureDialer.TLSClientConfig)
	require.True(t, insecureDialer.TLSClientConfig.InsecureSkipVerify)
}
