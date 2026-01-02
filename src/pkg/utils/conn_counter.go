package utils

import (
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

func init() {
	ConnCounterManager.bcMap = make(map[string]*ByteCounter)
}

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

func (m *ConnCounterManagerType) PrintMap() {
	m.mapLock.Lock()
	defer m.mapLock.Unlock()
	for url, counter := range m.bcMap {
		blog.GetLogger().Infof("host[%s] TCP bytes received: %s, sent: %s", url,
			FormatBytes(counter.ReadBytes), FormatBytes(counter.WriteBytes))
	}
}

// createTLSConfig creates a TLS configuration for the given host
// For edgesrv.com domains, it enables weak TLS 1.2 cipher suites for compatibility
func createTLSConfig(host string) *tls.Config {
	if strings.HasSuffix(host, ".edgesrv.com") || host == "edgesrv.com" {
		// Enable weak TLS 1.2 cipher suites for edgesrv.com
		return &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
			CipherSuites: []uint16{
				// Standard secure ciphers first
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				// Weak RSA cipher suites for compatibility with edgesrv.com
				tls.TLS_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_RSA_WITH_AES_256_CBC_SHA,
				tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
				tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
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
	// Check error message as fallback for other TLS errors
	errMsg := err.Error()
	return strings.Contains(errMsg, "tls:") || 
		strings.Contains(errMsg, "handshake") || 
		strings.Contains(errMsg, "certificate") ||
		strings.Contains(errMsg, "remote error")
}

func CreateDefaultClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}
	
	// Create TLS dialer with custom config
	dialTLS := func(network, addr string) (net.Conn, error) {
		// Extract hostname from addr (format is "host:port")
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		
		// Create TLS config
		tlsConfig := createTLSConfig(host)
		
		// Dial the connection
		conn, err := tls.DialWithDialer(dialer, network, addr, tlsConfig)
		if err != nil {
			// Log TLS errors with domain information
			if isTLSError(err) {
				blog.GetLogger().Errorf("TLS connection failed for domain %s: %v", host, err)
			}
			return nil, err
		}
		
		return conn, nil
	}
	
	transport := &http.Transport{
		Dial:    dialer.Dial,
		DialTLS: dialTLS,
	}
	return &http.Client{Transport: transport}
}

func CreateConnCounterClient() (*http.Client, error) {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}
	
	// Create TLS dialer with custom config
	dialTLS := func(network, addr string) (net.Conn, error) {
		// Extract hostname from addr (format is "host:port")
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		
		// Create TLS config
		tlsConfig := createTLSConfig(host)
		
		// Dial the connection
		conn, err := tls.DialWithDialer(dialer, network, addr, tlsConfig)
		if err != nil {
			// Log TLS errors with domain information
			if isTLSError(err) {
				blog.GetLogger().Errorf("TLS connection failed for domain %s: %v", host, err)
			}
			return nil, err
		}
		
		// Wrap with byte counter
		byteCounter := ConnCounterManager.GetConnCounter(addr)
		if byteCounter == nil {
			byteCounter = &ByteCounter{}
			ConnCounterManager.SetConn(addr, byteCounter)
		}
		bc := &connCounter{Conn: conn, ByteCounter: byteCounter}
		return bc, nil
	}
	
	dialPlain := func(network, addr string) (net.Conn, error) {
		conn, err := dialer.Dial(network, addr)
		if err != nil {
			return nil, err
		}

		byteCounter := ConnCounterManager.GetConnCounter(addr)
		if byteCounter == nil {
			byteCounter = &ByteCounter{}
			ConnCounterManager.SetConn(addr, byteCounter)
		}
		bc := &connCounter{Conn: conn, ByteCounter: byteCounter}
		return bc, nil
	}
	
	transport := &http.Transport{
		Dial:    dialPlain,
		DialTLS: dialTLS,
	}
	return &http.Client{Transport: transport}, nil
}
