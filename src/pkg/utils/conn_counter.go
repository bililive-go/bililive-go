package utils

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	blog "github.com/bililive-go/bililive-go/src/log"
)

type ByteCounter struct {
	ReadBytes  int64
	WriteBytes int64
}

type connCounter struct {
	net.Conn
	ByteCounter *ByteCounter
}

func (c *connCounter) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	c.ByteCounter.ReadBytes += int64(n)
	return
}

func (c *connCounter) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	c.ByteCounter.WriteBytes += int64(n)
	return
}

type ConnCounterManagerType struct {
	mapLock sync.Mutex
	bcMap   map[string]*ByteCounter
}

var ConnCounterManager ConnCounterManagerType

func (m *ConnCounterManagerType) SetConn(url string, bc *ByteCounter) {
	m.mapLock.Lock()
	defer m.mapLock.Unlock()
	m.bcMap[url] = bc
}

func (m *ConnCounterManagerType) GetConnCounter(url string) *ByteCounter {
	m.mapLock.Lock()
	defer m.mapLock.Unlock()
	bc, ok := m.bcMap[url]
	if !ok {
		return nil
	}
	return bc
}

// GetOrCreateConnCounter atomically gets or creates a ByteCounter for the given URL
// This ensures thread-safety by performing the check-then-act operation atomically
func (m *ConnCounterManagerType) GetOrCreateConnCounter(url string) *ByteCounter {
	m.mapLock.Lock()
	defer m.mapLock.Unlock()
	bc, ok := m.bcMap[url]
	if !ok {
		bc = &ByteCounter{}
		m.bcMap[url] = bc
	}
	return bc
}

func (m *ConnCounterManagerType) PrintMap() {
	m.mapLock.Lock()
	defer m.mapLock.Unlock()
	for url, counter := range m.bcMap {
		blog.GetLogger().Infof("host[%s] TCP bytes received: %s, sent: %s", url,
			FormatBytes(counter.ReadBytes), FormatBytes(counter.WriteBytes))
	}
}

var edgesrvWarningOnce sync.Once

// createTLSConfig creates a TLS configuration for the given host
// For edgesrv.com domains, it enables weak TLS 1.2 cipher suites for compatibility
func createTLSConfig(host string) *tls.Config {
	if strings.HasSuffix(host, ".edgesrv.com") || host == "edgesrv.com" {
		// Log warning only once to avoid log spam
		edgesrvWarningOnce.Do(func() {
			blog.GetLogger().Warnf("Enabling weak TLS 1.2 cipher suites for edgesrv.com domains. This may reduce connection security for these specific domains.")
		})
		
		// Enable weak TLS 1.2 cipher suites for edgesrv.com
		// Based on SSL Labs report, edgesrv.com servers require CBC-mode RSA cipher suites
		return &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
			CipherSuites: []uint16{
				// Standard secure ciphers first (prefer ECDHE for forward secrecy)
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				// Weak CBC-mode RSA cipher suites for compatibility with edgesrv.com
				tls.TLS_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_RSA_WITH_AES_256_CBC_SHA,
				tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
			},
		}
	}
	// For other domains, use default secure configuration
	return &tls.Config{
		ServerName: host,
	}
}

// isTLSError checks if the error is a TLS-related error
func isTLSError(err error) bool {
	if err == nil {
		return false
	}
	// Check for specific TLS error types
	var recordHeaderError tls.RecordHeaderError
	if errors.As(err, &recordHeaderError) {
		return true
	}
	var certVerifyErr *tls.CertificateVerificationError
	if errors.As(err, &certVerifyErr) {
		return true
	}
	// Check error message with more specific patterns to reduce false positives
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "tls: handshake") || 
		strings.Contains(errMsg, "tls handshake") || 
		strings.Contains(errMsg, "tls: bad certificate") ||
		strings.Contains(errMsg, "x509: certificate") ||
		strings.Contains(errMsg, "remote error: tls")
}

// extractHostname extracts the hostname from a network address (host:port)
func extractHostname(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return host
}

// createTLSDialer creates a TLS dialer function with custom TLS config and error logging
// The returned function can be used as Transport.DialTLSContext
func createTLSDialer(dialer *net.Dialer, withByteCounter bool, keyPrefix string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Extract hostname from addr
		host := extractHostname(addr)
		
		// Create TLS config
		tlsConfig := createTLSConfig(host)
		
		// First establish TCP connection with context support
		rawConn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		
		// Perform TLS handshake
		tlsConn := tls.Client(rawConn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			// Log TLS errors with domain information
			if isTLSError(err) {
				blog.GetLogger().Errorf("TLS connection failed for domain %s: %v", host, err)
			}
			return nil, err
		}
		
		// Wrap with byte counter if needed
		if withByteCounter {
			key := keyPrefix + addr
			byteCounter := ConnCounterManager.GetOrCreateConnCounter(key)
			return &connCounter{Conn: tlsConn, ByteCounter: byteCounter}, nil
		}
		
		return tlsConn, nil
	}
}

// newProductionTransport creates a http.Transport with production-ready configuration.
// The caller should set DialContext and DialTLSContext fields.
func newProductionTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func CreateDefaultClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}
	
	transport := newProductionTransport()
	transport.DialContext = dialer.DialContext
	transport.DialTLSContext = createTLSDialer(dialer, false, "")
	
	return &http.Client{Transport: transport}
}

func CreateConnCounterClient() (*http.Client, error) {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}
	
	// Plain TCP dialer with byte counter
	dialPlain := func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}

		// Use "plain:" prefix to distinguish from TLS connections
		key := "plain:" + addr
		byteCounter := ConnCounterManager.GetOrCreateConnCounter(key)
		bc := &connCounter{Conn: conn, ByteCounter: byteCounter}
		return bc, nil
	}
	
	transport := newProductionTransport()
	transport.DialContext = dialPlain
	// Use "tls:" prefix to distinguish from plain connections
	transport.DialTLSContext = createTLSDialer(dialer, true, "tls:")
	
	return &http.Client{Transport: transport}, nil
}
