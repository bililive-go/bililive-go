package utils

import (
	"os"
	"testing"
)

func TestInitDNSResolver(t *testing.T) {
	// Save original GODEBUG value
	originalGodebug := os.Getenv("GODEBUG")
	defer func() {
		if originalGodebug == "" {
			os.Unsetenv("GODEBUG")
		} else {
			os.Setenv("GODEBUG", originalGodebug)
		}
	}()

	// Test with empty GODEBUG
	os.Unsetenv("GODEBUG")
	InitDNSResolver()
	
	// On Windows, GODEBUG should be set to netdns=go
	// On other platforms, it should remain empty
	godebug := os.Getenv("GODEBUG")
	if godebug != "" && godebug != "netdns=go" {
		t.Errorf("Expected GODEBUG to be empty or 'netdns=go', got: %s", godebug)
	}

	// Test with existing GODEBUG value
	os.Setenv("GODEBUG", "gc=1")
	InitDNSResolver()
	
	godebug = os.Getenv("GODEBUG")
	if godebug != "gc=1" && godebug != "gc=1,netdns=go" {
		t.Errorf("Expected GODEBUG to be 'gc=1' or 'gc=1,netdns=go', got: %s", godebug)
	}
}

func TestCreateReliableHTTPClient(t *testing.T) {
	client := CreateReliableHTTPClient()
	if client == nil {
		t.Error("CreateReliableHTTPClient returned nil")
	}
	
	if client.Timeout.Seconds() != 60 {
		t.Errorf("Expected timeout of 60 seconds, got %v", client.Timeout.Seconds())
	}
}

func TestCreateReliableDialer(t *testing.T) {
	dialer := CreateReliableDialer()
	if dialer == nil {
		t.Error("CreateReliableDialer returned nil")
	}
	
	if dialer.Timeout.Seconds() != 30 {
		t.Errorf("Expected timeout of 30 seconds, got %v", dialer.Timeout.Seconds())
	}
}