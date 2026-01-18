// Package proxy 提供代理配置和管理功能
package proxy

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/bililive-go/bililive-go/src/configs"
	"golang.org/x/net/proxy"
)

// GetProxyURL 获取当前生效的代理 URL
// 优先级：配置文件 > 环境变量 (ALL_PROXY > HTTPS_PROXY > HTTP_PROXY)
func GetProxyURL() string {
	cfg := configs.GetCurrentConfig()
	if cfg != nil && cfg.Proxy.Enable && cfg.Proxy.URL != "" {
		return cfg.Proxy.URL
	}

	// 按优先级检查环境变量
	for _, envVar := range []string{"ALL_PROXY", "all_proxy", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if proxyURL := os.Getenv(envVar); proxyURL != "" {
			return proxyURL
		}
	}

	return ""
}

// GetProxyFunc 返回 HTTP 代理函数
// 用于 http.Transport.Proxy 字段
func GetProxyFunc() func(*http.Request) (*url.URL, error) {
	proxyURL := GetProxyURL()
	if proxyURL == "" {
		return nil
	}

	// SOCKS5 代理不能用于 HTTP Proxy 字段，需要通过 DialContext 处理
	if strings.HasPrefix(proxyURL, "socks5://") || strings.HasPrefix(proxyURL, "socks5h://") {
		return nil
	}

	// 解析代理 URL
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil
	}

	return http.ProxyURL(parsedURL)
}

// IsSocks5Proxy 检查当前代理是否为 SOCKS5 代理
func IsSocks5Proxy() bool {
	proxyURL := GetProxyURL()
	return strings.HasPrefix(proxyURL, "socks5://") || strings.HasPrefix(proxyURL, "socks5h://")
}

// CreateSocks5DialContext 为 SOCKS5 代理创建 DialContext 函数
// 用于 http.Transport.DialContext 字段
func CreateSocks5DialContext(proxyURL string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		// 解析代理 URL
		parsedURL, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}

		// 构建 SOCKS5 地址
		var auth *proxy.Auth
		if parsedURL.User != nil {
			auth = &proxy.Auth{
				User: parsedURL.User.Username(),
			}
			if password, ok := parsedURL.User.Password(); ok {
				auth.Password = password
			}
		}

		// 创建 SOCKS5 dialer
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}

		// 连接目标
		return dialer.Dial(network, addr)
	}
}

// ApplyProxyToTransport 将代理设置应用到 http.Transport
func ApplyProxyToTransport(transport *http.Transport) {
	proxyURL := GetProxyURL()
	if proxyURL == "" {
		// 使用系统默认代理（从环境变量）
		transport.Proxy = http.ProxyFromEnvironment
		return
	}

	if strings.HasPrefix(proxyURL, "socks5://") || strings.HasPrefix(proxyURL, "socks5h://") {
		// SOCKS5 代理需要通过 DialContext 处理
		transport.DialContext = CreateSocks5DialContext(proxyURL)
	} else {
		// HTTP/HTTPS 代理
		transport.Proxy = GetProxyFunc()
	}
}

// GetProxyEnvVars 返回用于子进程的代理环境变量
// 可用于传递给外部工具（如 ffmpeg、dotnet 等）
func GetProxyEnvVars() []string {
	proxyURL := GetProxyURL()
	if proxyURL == "" {
		return nil
	}

	return []string{
		"HTTP_PROXY=" + proxyURL,
		"HTTPS_PROXY=" + proxyURL,
		"http_proxy=" + proxyURL,
		"https_proxy=" + proxyURL,
	}
}
