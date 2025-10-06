# DNS Resolver Fix for Windows

## Issue
This fix addresses the "dial tcp: lookup api.live.bilibili.com: no such host" error that occurs on Windows systems approximately every 30 minutes.

## Root Cause
The issue is caused by Go's default behavior on Windows where it uses the system's DNS resolver, which can fail intermittently with "no such host" errors. This is a known issue documented in [golang/go#41425](https://github.com/golang/go/issues/41425#issuecomment-695883668).

## Solution
The fix forces Go to use its pure Go DNS resolver instead of the system DNS resolver by:

1. Setting `GODEBUG=netdns=go` environment variable on Windows systems
2. Using enhanced HTTP clients with better timeout configurations
3. Implementing reliable dialers throughout the application

## Files Modified
- `src/pkg/utils/dns_resolver.go` - New DNS resolver utilities
- `src/pkg/utils/dns_resolver_test.go` - Unit tests for DNS resolver
- `src/cmd/bililive/bililive.go` - Initialize DNS resolver on startup
- `src/live/internal/base_live.go` - Use reliable HTTP client
- `src/pkg/utils/conn_counter.go` - Enhanced connection handling

## Backward Compatibility
The fix is fully backward compatible and only affects Windows systems. On other platforms, the behavior remains unchanged.