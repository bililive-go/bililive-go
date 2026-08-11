package diagnostics

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

func randomHex(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

// openRegularFileNoSymlink 以“先 lstat、再 open、最后比较文件身份”的顺序
// 打开证据文件。仅检查路径字符串不能阻止 run 目录中的符号链接或
// lstat/open 之间的替换；SameFile 能确保最终读取的仍是刚检查过的普通文件。
func openRegularFileNoSymlink(path string) (*os.File, fs.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&fs.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, nil, errors.New("diagnostics 证据不是普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, nil, errors.New("diagnostics 证据在打开期间被替换")
	}
	return file, opened, nil
}

func readRegularFileNoSymlink(path string) ([]byte, error) {
	file, _, err := openRegularFileNoSymlink(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func ensureDirectoryNoSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("diagnostics 路径不是普通目录")
	}
	return nil
}

func atomicWriteJSON(path string, value any, replace bool) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(path, 0o600, replace, func(w io.Writer) error {
		return writeAll(w, data)
	})
}

func atomicWriteBytes(path string, data []byte, replace bool) error {
	return atomicWriteFile(path, 0o600, replace, func(w io.Writer) error {
		return writeAll(w, data)
	})
}

func atomicWriteFile(path string, mode fs.FileMode, replace bool, write func(io.Writer) error) (retErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	suffix, err := randomHex(8)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+filepath.Base(path)+".tmp-"+suffix)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}()

	if err = write(f); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}

	if replace {
		if err = os.Rename(tmp, path); err != nil {
			return err
		}
		return syncDir(dir)
	}

	// Link 的发布语义是原子的，并且不会覆盖已经存在的 marker。
	if err = os.Link(tmp, path); err == nil {
		if removeErr := os.Remove(tmp); removeErr != nil {
			return removeErr
		}
		return syncDir(dir)
	}
	if errors.Is(err, fs.ErrExist) {
		return err
	}

	// 某些文件系统不支持硬链接。目标目录和文件名都只属于本 run，
	// 因此在再次确认不存在后使用 rename，仍不会覆盖正常创建的证据。
	var errno syscall.Errno
	if !errors.As(err, &errno) ||
		(errno != syscall.EPERM && errno != syscall.ENOTSUP && errno != syscall.EOPNOTSUPP) {
		return err
	}
	if _, statErr := os.Lstat(path); statErr == nil {
		return fs.ErrExist
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return statErr
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(dir)
}
