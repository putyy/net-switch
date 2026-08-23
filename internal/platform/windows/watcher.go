//go:build windows

package windows

import (
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ipHelperDLL             = windows.NewLazySystemDLL("iphlpapi.dll")
	notifyIPInterfaceChange = ipHelperDLL.NewProc("NotifyIpInterfaceChange")
	cancelMibChangeNotify2  = ipHelperDLL.NewProc("CancelMibChangeNotify2")
	watcherRegistry         sync.Map
	nextWatcherID           atomic.Uintptr
	networkChangeCallback   = syscall.NewCallback(networkInterfaceChanged)
)

type Watcher struct {
	id     uintptr
	handle windows.Handle
	events chan struct{}
	close  sync.Once
}

func NewWatcher() (*Watcher, error) {
	watcher := &Watcher{
		id:     nextWatcherID.Add(1),
		events: make(chan struct{}, 1),
	}
	watcherRegistry.Store(watcher.id, watcher)
	result, _, callErr := notifyIPInterfaceChange.Call(
		0,
		networkChangeCallback,
		watcher.id,
		0,
		uintptr(unsafe.Pointer(&watcher.handle)),
	)
	if result != 0 {
		watcherRegistry.Delete(watcher.id)
		return nil, fmt.Errorf("register Windows network change notification (code %d): %v", result, callErr)
	}
	return watcher, nil
}

func (w *Watcher) Events() <-chan struct{} {
	return w.events
}

func (w *Watcher) Close() error {
	var closeErr error
	w.close.Do(func() {
		result, _, callErr := cancelMibChangeNotify2.Call(uintptr(w.handle))
		watcherRegistry.Delete(w.id)
		if result != 0 {
			closeErr = fmt.Errorf("cancel Windows network change notification (code %d): %v", result, callErr)
		}
	})
	return closeErr
}

func networkInterfaceChanged(context uintptr, _ uintptr, _ uintptr) uintptr {
	value, ok := watcherRegistry.Load(context)
	if !ok {
		return 0
	}
	watcher := value.(*Watcher)
	select {
	case watcher.events <- struct{}{}:
	default:
	}
	return 0
}
