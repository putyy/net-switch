//go:build darwin && cgo

package darwin

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration -framework CoreLocation -framework CoreWLAN -framework Cocoa -framework Foundation
#include "watcher.h"
#include "wifi.h"
*/
import "C"

import (
	"errors"
	"runtime/cgo"
	"sync"
)

type Watcher struct {
	events   chan struct{}
	handle   cgo.Handle
	native   *C.NetSwitchWatcher
	location *C.NetSwitchLocationObserver
	close    sync.Once
}

func NewWatcher() (*Watcher, error) {
	watcher := &Watcher{events: make(chan struct{}, 1)}
	watcher.handle = cgo.NewHandle(watcher)
	watcher.native = C.NetSwitchWatcherCreate(C.uintptr_t(watcher.handle))
	if watcher.native == nil {
		watcher.handle.Delete()
		return nil, errors.New("could not register for macOS network change notifications")
	}
	watcher.location = C.NetSwitchLocationObserverCreate(C.uintptr_t(watcher.handle))
	return watcher, nil
}

func (w *Watcher) Events() <-chan struct{} {
	return w.events
}

func (w *Watcher) RequestPermissions() {
	C.NetSwitchLocationObserverRequestAuthorization(w.location)
}

func (w *Watcher) Close() error {
	w.close.Do(func() {
		C.NetSwitchLocationObserverStop(w.location)
		w.location = nil
		C.NetSwitchWatcherStop(w.native)
		w.native = nil
		w.handle.Delete()
		close(w.events)
	})
	return nil
}

//export netSwitchLocationAuthorizationChanged
func netSwitchLocationAuthorizationChanged(handle C.uintptr_t) {
	watcher, ok := cgo.Handle(handle).Value().(*Watcher)
	if ok {
		watcher.notify()
	}
}

func (w *Watcher) notify() {
	select {
	case w.events <- struct{}{}:
	default:
	}
}

//export netSwitchNetworkChanged
func netSwitchNetworkChanged(handle C.uintptr_t) {
	watcher, ok := cgo.Handle(handle).Value().(*Watcher)
	if ok {
		watcher.notify()
	}
}
