//go:build windows

package diagnostics

// syncDir 在 Windows 上是 best-effort no-op。atomicWriteFile 已经对临时
// 文件调用 Sync；而用 os.Open 打开的目录句柄通常不具备 FlushFileBuffers
// 所需权限，强行 Sync 会让每次原子发布都以 ERROR_ACCESS_DENIED 失败。
func syncDir(string) error {
	return nil
}
