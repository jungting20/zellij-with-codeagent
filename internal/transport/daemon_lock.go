package transport

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// AcquireDaemonLock reserves a socket before recovery or background work starts.
// Keep the returned file open until shutdown finishes. Never unlink the lock
// file: another process could otherwise lock a different inode at the same path.
// This is separate from the client's short-lived .start.lock.
func AcquireDaemonLock(socketPath string) (*os.File, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("transport: socket path is required")
	}
	path, err := filepath.Abs(socketPath)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	path = filepath.Join(parent, filepath.Base(path))
	file, err := os.OpenFile(path+".daemon.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("daemon already running or socket %s is locked: %w", socketPath, err)
	}
	return file, nil
}
