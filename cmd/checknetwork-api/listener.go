package main

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const connectionRejectionBackoff = 5 * time.Millisecond

type boundedListener struct {
	net.Listener
	leases    chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	rejected  atomic.Uint64
}

func newBoundedListener(listener net.Listener, limit int) (*boundedListener, error) {
	if listener == nil {
		return nil, fmt.Errorf("listener is required")
	}
	if limit < 1 || limit > maxConnectionsLimit {
		return nil, fmt.Errorf("connection limit must be between 1 and %d", maxConnectionsLimit)
	}
	return &boundedListener{
		Listener: listener,
		leases:   make(chan struct{}, limit),
		done:     make(chan struct{}),
	}, nil
}

func (listener *boundedListener) Accept() (net.Conn, error) {
	for {
		conn, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case listener.leases <- struct{}{}:
			return &leasedConn{Conn: conn, release: func() { <-listener.leases }}, nil
		default:
			listener.rejected.Add(1)
			_ = conn.Close()
			timer := time.NewTimer(connectionRejectionBackoff)
			select {
			case <-timer.C:
			case <-listener.done:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return nil, net.ErrClosed
			}
		}
	}
}

func (listener *boundedListener) Close() error {
	var err error
	listener.closeOnce.Do(func() {
		close(listener.done)
		err = listener.Listener.Close()
	})
	return err
}

func (listener *boundedListener) Active() int      { return len(listener.leases) }
func (listener *boundedListener) Rejected() uint64 { return listener.rejected.Load() }

type leasedConn struct {
	net.Conn
	releaseOnce sync.Once
	release     func()
}

func (conn *leasedConn) Close() error {
	defer conn.releaseOnce.Do(conn.release)
	return conn.Conn.Close()
}

func serveHTTP(server *http.Server, listener net.Listener) error {
	return server.Serve(listener)
}
