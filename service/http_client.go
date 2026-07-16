package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gorilla/websocket"
	"golang.org/x/net/proxy"
)

var (
	httpClient      *http.Client
	clientCacheLock sync.Mutex
	channelClients  = make(map[httpClientCacheKey]*http.Client)
)

type httpClientCacheKey struct {
	proxyURL           string
	insecureSkipVerify bool
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	fetchSetting := system_setting.GetFetchSetting()
	urlStr := req.URL.String()
	if err := common.ValidateURLWithFetchSetting(urlStr, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

func InitHttpClient() {
	client, err := newRelayHTTPClient("", common.TLSInsecureSkipVerify)
	if err != nil {
		panic(fmt.Sprintf("initialize relay http client: %v", err))
	}
	httpClient = client
	ResetProxyClientCache()
}

func GetHttpClient() *http.Client {
	return httpClient
}

// GetHttpClientWithProxy returns the default client or a proxy-enabled one when proxyURL is provided.
func GetHttpClientWithProxy(proxyURL string) (*http.Client, error) {
	return GetHttpClientWithOptions(proxyURL, false)
}

// GetHttpClientWithOptions returns a relay HTTP client honoring channel-level
// proxy and TLS settings. The per-channel TLS bypass is isolated in its own
// transport so it never mutates the process-wide secure client.
func GetHttpClientWithOptions(proxyURL string, insecureSkipVerify bool) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	effectiveInsecureSkipVerify := common.TLSInsecureSkipVerify || insecureSkipVerify
	if proxyURL == "" && effectiveInsecureSkipVerify == common.TLSInsecureSkipVerify {
		if client := GetHttpClient(); client != nil {
			return client, nil
		}
	}

	key := httpClientCacheKey{
		proxyURL:           proxyURL,
		insecureSkipVerify: effectiveInsecureSkipVerify,
	}
	clientCacheLock.Lock()
	defer clientCacheLock.Unlock()
	if client, ok := channelClients[key]; ok {
		return client, nil
	}
	client, err := newRelayHTTPClient(proxyURL, effectiveInsecureSkipVerify)
	if err != nil {
		return nil, err
	}
	channelClients[key] = client
	return client, nil
}

// ResetProxyClientCache 清空代理客户端缓存，确保下次使用时重新初始化
func ResetProxyClientCache() {
	clientCacheLock.Lock()
	defer clientCacheLock.Unlock()
	for _, client := range channelClients {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	channelClients = make(map[httpClientCacheKey]*http.Client)
}

// NewProxyHttpClient 创建支持代理的 HTTP 客户端
func NewProxyHttpClient(proxyURL string) (*http.Client, error) {
	return GetHttpClientWithOptions(proxyURL, false)
}

// GetWebsocketDialerWithOptions builds an isolated WebSocket dialer with the
// same channel-level proxy and TLS behavior as relay HTTP clients.
func GetWebsocketDialerWithOptions(proxyURL string, insecureSkipVerify bool) (*websocket.Dialer, error) {
	dialer := *websocket.DefaultDialer
	if common.TLSInsecureSkipVerify || insecureSkipVerify {
		dialer.TLSClientConfig = common.InsecureTLSConfig.Clone()
	}

	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		dialer.Proxy = http.ProxyFromEnvironment
		return &dialer, nil
	}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	switch parsedURL.Scheme {
	case "http", "https":
		dialer.Proxy = http.ProxyURL(parsedURL)
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if parsedURL.User != nil {
			auth = &proxy.Auth{User: parsedURL.User.Username()}
			if password, ok := parsedURL.User.Password(); ok {
				auth.Password = password
			}
		}
		socksDialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		dialer.Proxy = nil
		dialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return socksDialer.Dial(network, addr)
		}
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s, must be http, https, socks5 or socks5h", parsedURL.Scheme)
	}
	return &dialer, nil
}

func newRelayHTTPClient(proxyURL string, insecureSkipVerify bool) (*http.Client, error) {
	transport := &http.Transport{
		MaxIdleConns:        common.RelayMaxIdleConns,
		MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
		ForceAttemptHTTP2:   true,
	}
	if insecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig.Clone()
	}

	if proxyURL == "" {
		transport.Proxy = http.ProxyFromEnvironment
	} else {
		parsedURL, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		switch parsedURL.Scheme {
		case "http", "https":
			transport.Proxy = http.ProxyURL(parsedURL)
		case "socks5", "socks5h":
			// 获取认证信息
			var auth *proxy.Auth
			if parsedURL.User != nil {
				auth = &proxy.Auth{
					User:     parsedURL.User.Username(),
					Password: "",
				}
				if password, ok := parsedURL.User.Password(); ok {
					auth.Password = password
				}
			}

			// 创建 SOCKS5 代理拨号器
			// proxy.SOCKS5 使用 tcp 参数，所有 TCP 连接包括 DNS 查询都将通过代理进行。行为与 socks5h 相同
			dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)
			if err != nil {
				return nil, err
			}

			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		default:
			return nil, fmt.Errorf("unsupported proxy scheme: %s, must be http, https, socks5 or socks5h", parsedURL.Scheme)
		}
	}

	client := &http.Client{Transport: transport, CheckRedirect: checkRedirect}
	if common.RelayTimeout > 0 {
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
	}
	return client, nil
}
