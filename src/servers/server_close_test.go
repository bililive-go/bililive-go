package servers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bililive-go/bililive-go/src/instance"
)

func TestServerCloseAcceptsCancelledRootContextAndSignalsDoneOnce(t *testing.T) {
	inst := &instance.Instance{}
	root := context.WithValue(context.Background(), instance.Key, inst)
	root, cancel := context.WithCancel(root)
	inst.WaitGroup.Add(1)

	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	httpServer := &http.Server{Handler: http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		close(requestEntered)
		<-releaseRequest
		w.WriteHeader(http.StatusNoContent)
	})}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- httpServer.Serve(listener)
	}()

	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		response, requestErr := client.Get("http://" + listener.Addr().String())
		if response != nil {
			_ = response.Body.Close()
		}
		requestDone <- requestErr
	}()
	select {
	case <-requestEntered:
	case <-time.After(time.Second):
		t.Fatal("测试请求没有进入 HTTP handler")
	}

	server := &Server{server: httpServer}
	cancel()

	closeReturned := make(chan struct{})
	go func() {
		server.Close(root)
		close(closeReturned)
	}()

	select {
	case <-closeReturned:
		t.Fatal("root context 已取消时，Shutdown 不应立即跳过活跃请求")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseRequest)
	select {
	case <-closeReturned:
	case <-time.After(time.Second):
		t.Fatal("活跃请求结束后 HTTP Shutdown 没有返回")
	}
	require.NoError(t, <-requestDone)
	serveErr := <-serveDone
	assert.True(t, errors.Is(serveErr, http.ErrServerClosed))

	assert.NotPanics(t, func() {
		server.Close(root)
	})

	done := make(chan struct{})
	go func() {
		inst.WaitGroup.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("HTTP Shutdown 完成后没有调用 WaitGroup.Done")
	}
}
