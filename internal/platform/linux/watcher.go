//go:build linux

package linux

import (
	"sync"

	"github.com/godbus/dbus/v5"
)

type Watcher struct {
	connection *dbus.Conn
	signals    chan *dbus.Signal
	events     chan struct{}
	done       chan struct{}
	close      sync.Once
	wait       sync.WaitGroup
}

func NewWatcher() (*Watcher, error) {
	connection, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	if err := connection.AddMatchSignal(dbus.WithMatchSender("org.freedesktop.NetworkManager")); err != nil {
		connection.Close()
		return nil, err
	}
	watcher := &Watcher{
		connection: connection,
		signals:    make(chan *dbus.Signal, 16),
		events:     make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
	connection.Signal(watcher.signals)
	watcher.wait.Add(1)
	go watcher.run()
	return watcher, nil
}

func (w *Watcher) Events() <-chan struct{} {
	return w.events
}

func (w *Watcher) Close() error {
	w.close.Do(func() {
		close(w.done)
		w.connection.RemoveSignal(w.signals)
		_ = w.connection.RemoveMatchSignal(dbus.WithMatchSender("org.freedesktop.NetworkManager"))
		w.connection.Close()
		w.wait.Wait()
		close(w.events)
	})
	return nil
}

func (w *Watcher) run() {
	defer w.wait.Done()
	for {
		select {
		case <-w.done:
			return
		case signal := <-w.signals:
			if signal == nil {
				continue
			}
			select {
			case w.events <- struct{}{}:
			default:
			}
		}
	}
}
