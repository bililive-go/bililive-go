package openlist

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRemoteManager(t *testing.T) {
	mgr := NewRemoteManager("https://openlist.example.com/")

	assert.True(t, mgr.IsExternal())
	assert.Equal(t, "https://openlist.example.com", mgr.GetAPIEndpoint())
	assert.Equal(t, 0, mgr.GetPort())
	assert.Empty(t, mgr.GetDataPath())
}

func TestNewManagerUsesLocalEndpoint(t *testing.T) {
	mgr := NewManager("data", 5244)

	assert.False(t, mgr.IsExternal())
	assert.Equal(t, "http://127.0.0.1:5244", mgr.GetAPIEndpoint())
}
