package utils

import (
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

func (m *ConnCounterManagerType) PrintMap() {
	m.mapLock.Lock()
	defer m.mapLock.Unlock()
	for url, counter := range m.bcMap {
		blog.GetLogger().Infof("host[%s] TCP bytes received: %s, sent: %s", url,
			FormatBytes(counter.ReadBytes), FormatBytes(counter.WriteBytes))
	}
}

// ConnStats 表示单个主机的连接统计信息
type ConnStats struct {
	Host           string `json:"host"`
	ReceivedBytes  int64  `json:"received_bytes"`
	ReceivedFormat string `json:"received_format"`
	SentBytes      int64  `json:"sent_bytes"`
	SentFormat     string `json:"sent_format"`
}

// GetAllStats 返回所有主机的连接统计信息
func (m *ConnCounterManagerType) GetAllStats() []ConnStats {
	m.mapLock.Lock()
	defer m.mapLock.Unlock()
	stats := make([]ConnStats, 0, len(m.bcMap))
	for host, counter := range m.bcMap {
		stats = append(stats, ConnStats{
			Host:           host,
			ReceivedBytes:  counter.ReadBytes,
			ReceivedFormat: FormatBytes(counter.ReadBytes),
			SentBytes:      counter.WriteBytes,
			SentFormat:     FormatBytes(counter.WriteBytes),
		})
	}
	return stats
}

// GetStatsByHost 返回指定主机的连接统计信息，支持前缀匹配
func (m *ConnCounterManagerType) GetStatsByHostPrefix(prefix string) []ConnStats {
	m.mapLock.Lock()
	defer m.mapLock.Unlock()
	stats := make([]ConnStats, 0)
	for host, counter := range m.bcMap {
		if strings.Contains(host, prefix) {
			stats = append(stats, ConnStats{
				Host:           host,
				ReceivedBytes:  counter.ReadBytes,
				ReceivedFormat: FormatBytes(counter.ReadBytes),
				SentBytes:      counter.WriteBytes,
				SentFormat:     FormatBytes(counter.WriteBytes),
			})
		}
	}
	return stats
}

func CreateConnCounterClient() (*http.Client, error) {
	dialer := func(network, addr string) (net.Conn, error) {
		conn, err := net.DialTimeout(network, addr, 10*time.Second)
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
		Dial: dialer,
	}
	return &http.Client{Transport: transport}, nil
}
