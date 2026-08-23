//go:build darwin || linux

package instance

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	openCommand       = "open"
	notifyAttempts    = 20
	notifyRetryDelay  = 50 * time.Millisecond
	connectionTimeout = time.Second
)

var ErrAlreadyRunning = errors.New("Net Switch is already running")

type Instance struct {
	listener   *net.UnixListener
	lockFile   *os.File
	socketPath string
	requests   chan struct{}
	closeOnce  sync.Once
	closeErr   error
}

func Acquire(notifyOpen bool) (*Instance, error) {
	runtimeDir, err := runtimeDirectory()
	if err != nil {
		return nil, err
	}

	lockPath := filepath.Join(runtimeDir, "instance.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}

	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lockFile.Close()
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("acquire instance lock: %w", err)
		}
		if notifyOpen {
			if notifyErr := notifyExisting(filepath.Join(runtimeDir, "instance.sock")); notifyErr != nil {
				return nil, fmt.Errorf("existing instance did not respond: %w", notifyErr)
			}
		}
		return nil, ErrAlreadyRunning
	}

	socketPath := filepath.Join(runtimeDir, "instance.sock")
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		releaseLock(lockFile)
		return nil, fmt.Errorf("remove stale instance socket: %w", err)
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		releaseLock(lockFile)
		return nil, fmt.Errorf("listen on instance socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		releaseLock(lockFile)
		return nil, fmt.Errorf("set instance socket permissions: %w", err)
	}

	running := &Instance{
		listener:   listener,
		lockFile:   lockFile,
		socketPath: socketPath,
		requests:   make(chan struct{}, 1),
	}
	go running.acceptRequests()
	return running, nil
}

func (i *Instance) OpenRequests() <-chan struct{} {
	return i.requests
}

func (i *Instance) Close() error {
	i.closeOnce.Do(func() {
		if err := i.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			i.closeErr = err
		}
		if err := os.Remove(i.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) && i.closeErr == nil {
			i.closeErr = err
		}
		if err := unix.Flock(int(i.lockFile.Fd()), unix.LOCK_UN); err != nil && i.closeErr == nil {
			i.closeErr = err
		}
		if err := i.lockFile.Close(); err != nil && i.closeErr == nil {
			i.closeErr = err
		}
	})
	return i.closeErr
}

func (i *Instance) acceptRequests() {
	for {
		connection, err := i.listener.AcceptUnix()
		if err != nil {
			return
		}
		i.handleConnection(connection)
	}
}

func (i *Instance) handleConnection(connection *net.UnixConn) {
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(connectionTimeout))

	buffer := make([]byte, 32)
	count, err := connection.Read(buffer)
	if err != nil || string(bytes.TrimSpace(buffer[:count])) != openCommand {
		return
	}

	select {
	case i.requests <- struct{}{}:
	default:
	}
}

func runtimeDirectory() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("get user cache directory: %w", err)
	}

	runtimeDir := filepath.Join(cacheDir, "Net Switch")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("create runtime directory: %w", err)
	}
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("set runtime directory permissions: %w", err)
	}
	return runtimeDir, nil
}

func notifyExisting(socketPath string) error {
	var lastErr error
	for attempt := 0; attempt < notifyAttempts; attempt++ {
		connection, err := net.DialTimeout("unix", socketPath, connectionTimeout)
		if err == nil {
			_ = connection.SetWriteDeadline(time.Now().Add(connectionTimeout))
			_, writeErr := connection.Write([]byte(openCommand + "\n"))
			closeErr := connection.Close()
			if writeErr == nil {
				return closeErr
			}
			lastErr = writeErr
		} else {
			lastErr = err
		}
		time.Sleep(notifyRetryDelay)
	}
	return lastErr
}

func releaseLock(lockFile *os.File) {
	_ = unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
	_ = lockFile.Close()
}
