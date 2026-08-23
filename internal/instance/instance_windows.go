//go:build windows

package instance

import (
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

var ErrAlreadyRunning = errors.New("Net Switch is already running")

type Instance struct {
	handle   windows.Handle
	requests chan struct{}
	close    sync.Once
	closeErr error
}

func Acquire(_ bool) (*Instance, error) {
	name, err := windows.UTF16PtrFromString(`Local\com.putyy.net-switch`)
	if err != nil {
		return nil, fmt.Errorf("create instance mutex name: %w", err)
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return nil, fmt.Errorf("create instance mutex: %w", err)
	}
	if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(handle)
		return nil, ErrAlreadyRunning
	}
	return &Instance{handle: handle, requests: make(chan struct{})}, nil
}

func (i *Instance) OpenRequests() <-chan struct{} {
	return i.requests
}

func (i *Instance) Close() error {
	i.close.Do(func() {
		close(i.requests)
		if err := windows.CloseHandle(i.handle); err != nil {
			i.closeErr = fmt.Errorf("close instance mutex: %w", err)
		}
	})
	return i.closeErr
}
