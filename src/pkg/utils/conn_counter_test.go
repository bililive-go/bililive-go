package utils

import (
	"crypto/tls"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateTLSConfig(t *testing.T) {
	tests := []struct {
		name                string
		host                string
		expectWeakCiphers   bool
		expectMinTLS12      bool
	}{
		{
			name:                "edgesrv.com exact match",
			host:                "edgesrv.com",
			expectWeakCiphers:   true,
			expectMinTLS12:      true,
		},
		{
			name:                "subdomain of edgesrv.com",
			host:                "stream-shanghai-ct-61-172-246-239.edgesrv.com",
			expectWeakCiphers:   true,
			expectMinTLS12:      true,
		},
		{
			name:                "another subdomain of edgesrv.com",
			host:                "cdn.edgesrv.com",
			expectWeakCiphers:   true,
			expectMinTLS12:      true,
		},
		{
			name:                "non-edgesrv.com domain",
			host:                "example.com",
			expectWeakCiphers:   false,
			expectMinTLS12:      false,
		},
		{
			name:                "domain ending with edgesrv.com but not subdomain",
			host:                "fakeedgesrv.com",
			expectWeakCiphers:   false,
			expectMinTLS12:      false,
		},
		{
			name:                "empty host",
			host:                "",
			expectWeakCiphers:   false,
			expectMinTLS12:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := createTLSConfig(tt.host)
			
			assert.NotNil(t, config)
			assert.Equal(t, tt.host, config.ServerName)
			
			if tt.expectWeakCiphers {
				// Check for weak cipher suites
				assert.NotNil(t, config.CipherSuites)
				assert.Greater(t, len(config.CipherSuites), 0)
				
				// Check that weak RSA ciphers are included
				foundWeakCipher := false
				for _, cipher := range config.CipherSuites {
					if cipher == tls.TLS_RSA_WITH_AES_128_CBC_SHA ||
						cipher == tls.TLS_RSA_WITH_AES_256_CBC_SHA ||
						cipher == tls.TLS_RSA_WITH_AES_128_CBC_SHA256 {
						foundWeakCipher = true
						break
					}
				}
				assert.True(t, foundWeakCipher, "Expected to find weak cipher suites for edgesrv.com")
			} else {
				// For non-edgesrv.com domains, cipher suites should be nil (use default)
				assert.Nil(t, config.CipherSuites)
			}
			
			if tt.expectMinTLS12 {
				assert.Equal(t, uint16(tls.VersionTLS12), config.MinVersion)
			} else {
				assert.Equal(t, uint16(0), config.MinVersion)
			}
		})
	}
}

func TestIsTLSError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "TLS handshake error",
			err:      errors.New("tls: handshake failure"),
			expected: true,
		},
		{
			name:     "TLS handshake error (uppercase)",
			err:      errors.New("TLS handshake failed"),
			expected: true,
		},
		{
			name:     "TLS bad certificate error",
			err:      errors.New("tls: bad certificate"),
			expected: true,
		},
		{
			name:     "x509 certificate error",
			err:      errors.New("x509: certificate signed by unknown authority"),
			expected: true,
		},
		{
			name:     "remote TLS error",
			err:      errors.New("remote error: tls: internal error"),
			expected: true,
		},
		{
			name:     "RecordHeaderError",
			err:      tls.RecordHeaderError{Msg: "bad record MAC"},
			expected: true,
		},
		{
			name:     "non-TLS error",
			err:      errors.New("connection refused"),
			expected: false,
		},
		{
			name:     "generic network error",
			err:      errors.New("dial tcp: i/o timeout"),
			expected: false,
		},
		{
			name:     "URL with https (should not match)",
			err:      errors.New("failed to fetch https://example.com"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTLSError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractHostname(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		expected string
	}{
		{
			name:     "host with port",
			addr:     "example.com:443",
			expected: "example.com",
		},
		{
			name:     "IP with port",
			addr:     "192.168.1.1:8080",
			expected: "192.168.1.1",
		},
		{
			name:     "IPv6 with port",
			addr:     "[2001:db8::1]:443",
			expected: "2001:db8::1",
		},
		{
			name:     "host without port",
			addr:     "example.com",
			expected: "example.com",
		},
		{
			name:     "empty string",
			addr:     "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractHostname(tt.addr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCreateDefaultClient(t *testing.T) {
	client := CreateDefaultClient()
	
	assert.NotNil(t, client)
	assert.NotNil(t, client.Transport)
	
	transport, ok := client.Transport.(*http.Transport)
	assert.True(t, ok, "Transport should be *http.Transport")
	
	// Verify transport configuration
	assert.NotNil(t, transport.DialContext)
	assert.NotNil(t, transport.DialTLSContext)
	assert.Equal(t, 100, transport.MaxIdleConns)
	assert.Equal(t, 10, transport.MaxIdleConnsPerHost)
	assert.Greater(t, transport.IdleConnTimeout.Seconds(), 0.0)
	assert.Greater(t, transport.TLSHandshakeTimeout.Seconds(), 0.0)
	assert.Greater(t, transport.ResponseHeaderTimeout.Seconds(), 0.0)
}

func TestCreateConnCounterClient(t *testing.T) {
	client, err := CreateConnCounterClient()
	
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.NotNil(t, client.Transport)
	
	transport, ok := client.Transport.(*http.Transport)
	assert.True(t, ok, "Transport should be *http.Transport")
	
	// Verify transport configuration
	assert.NotNil(t, transport.DialContext)
	assert.NotNil(t, transport.DialTLSContext)
	assert.Equal(t, 100, transport.MaxIdleConns)
	assert.Equal(t, 10, transport.MaxIdleConnsPerHost)
	assert.Greater(t, transport.IdleConnTimeout.Seconds(), 0.0)
	assert.Greater(t, transport.TLSHandshakeTimeout.Seconds(), 0.0)
	assert.Greater(t, transport.ResponseHeaderTimeout.Seconds(), 0.0)
}
