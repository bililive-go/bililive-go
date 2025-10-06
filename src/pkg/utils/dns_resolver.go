package utils

import (
	"net"
	"net/http"
	"os"
	"runtime"
	"time"
)

// InitDNSResolver configures Go to use pure Go DNS resolver to fix
// DNS lookup issues on Windows systems where the default system DNS
// resolver can fail intermittently with "no such host" errors.
// 
// See: https://github.com/golang/go/issues/41425#issuecomment-695883668
func InitDNSResolver() {
	// On Windows, force Go to use its pure Go DNS resolver instead of
	// the system DNS resolver to avoid intermittent lookup failures
	if runtime.GOOS == "windows" {
		// Set GODEBUG=netdns=go to force pure Go DNS resolver
		if os.Getenv("GODEBUG") == "" {
			os.Setenv("GODEBUG", "netdns=go")
		} else {
			// Append to existing GODEBUG value
			existing := os.Getenv("GODEBUG")
			if existing != "" && existing[len(existing)-1] != ',' {
				existing += ","
			}
			os.Setenv("GODEBUG", existing+"netdns=go")
		}
	}
}

// CreateReliableDialer creates a net.Dialer with enhanced DNS resolution
// settings for better reliability on Windows systems
func CreateReliableDialer() *net.Dialer {
	return &net.Dialer{
		// Use a reasonable timeout for DNS lookups and connections
		Timeout: 30 * time.Second,
	}
}

// CreateReliableHTTPClient creates an HTTP client with reliable DNS resolution
// for better stability on Windows systems
func CreateReliableHTTPClient() *http.Client {
	dialer := CreateReliableDialer()
	
	transport := &http.Transport{
		Dial: dialer.Dial,
		// Additional transport settings for better reliability
		DisableKeepAlives:     false,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	
	return &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second, // Overall request timeout
	}
}