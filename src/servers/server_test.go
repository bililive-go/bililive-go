package servers

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/stretchr/testify/require"
)

func TestServerStartReturnsListenError(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{})
	server := &Server{
		server: &http.Server{
			Addr:    listener.Addr().String(),
			Handler: http.NewServeMux(),
		},
	}

	err = server.Start(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "address already in use")
}
