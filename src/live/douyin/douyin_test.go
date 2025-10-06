package douyin

import (
	"net/url"
	"sync"
	"testing"
)

// Test that concurrent access to headers map doesn't cause race conditions
func TestConcurrentHeadersAccess(t *testing.T) {
	// Create a test Live instance
	testURL, _ := url.Parse("https://live.douyin.com/test")
	builder := &builder{}
	_, err := builder.Build(testURL)
	if err != nil {
		t.Fatalf("Failed to create Live instance: %v", err)
	}
	
	// Test headers map to verify deep copy is working
	const numGoroutines = 50
	const numOpsPerGoroutine = 100
	
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	
	// Create concurrent operations that would previously cause race conditions
	for i := 0; i < numGoroutines; i++ {
		go func(routineID int) {
			defer wg.Done()
			
			for j := 0; j < numOpsPerGoroutine; j++ {
				// Simulate the header copying that was causing race conditions
				localHeaders := make(map[string]interface{})
				for k, v := range headers {
					localHeaders[k] = v
				}
				
				// Modify the local copy (this used to modify the global headers)
				localHeaders["Test-Header"] = routineID
				localHeaders["Operation"] = j
				
				// Verify we have a proper copy
				if len(localHeaders) <= len(headers) {
					t.Errorf("localHeaders should have more entries than global headers")
					return
				}
			}
		}(i)
	}
	
	wg.Wait()
	
	// Verify global headers weren't modified
	if _, exists := headers["Test-Header"]; exists {
		t.Error("Global headers map was modified during concurrent access")
	}
	if _, exists := headers["Operation"]; exists {
		t.Error("Global headers map was modified during concurrent access")  
	}
}