package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func dialPartialHeader(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET /api/v1/health HTTP/1.1\r\nHost: partial"); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn
}

func TestConnectionAdmissionExactLimitPlusOneAndCapacityRecovery(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := newBoundedListener(raw, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	clients := make([]net.Conn, 3)
	for index := range clients {
		clients[index], err = net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, conn := range clients {
			if conn != nil {
				conn.Close()
			}
		}
	}()
	first, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	second, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	waitFor(t, time.Second, func() bool { return listener.Active() == 2 && listener.Rejected() >= 1 }, "limit+1 rejection")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	clients = append(clients, replacement)
	select {
	case recovered := <-accepted:
		recovered.Close()
	case <-time.After(time.Second):
		t.Fatal("capacity did not recover after admitted connection closed")
	}
	second.Close()
	waitFor(t, time.Second, func() bool { return listener.Active() == 0 }, "all leases released")
}

func TestConnectionAdmissionExact256PartialHeadersBoundsProductionServePath(t *testing.T) {
	const hostileConnections = 256
	const limit = 128
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := newBoundedListener(raw, limit)
	if err != nil {
		t.Fatal(err)
	}
	server := newHTTPServer(raw.Addr().String(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("partial header reached handler")
	}))
	server.ReadHeaderTimeout = 30 * time.Second
	serveDone := make(chan error, 1)
	baseline := runtime.NumGoroutine()
	go func() { serveDone <- serveHTTP(server, listener) }()

	clients := make([]net.Conn, 0, hostileConnections)
	for index := 0; index < hostileConnections; index++ {
		clients = append(clients, dialPartialHeader(t, raw.Addr().String()))
	}
	defer func() {
		for _, conn := range clients {
			if conn != nil {
				_ = conn.Close()
			}
		}
	}()
	waitFor(t, 3*time.Second, func() bool { return listener.Active() == limit && listener.Rejected() >= hostileConnections-limit }, "all 256 partial headers classified")
	if got := listener.Active(); got != limit {
		t.Fatalf("active=%d want=%d", got, limit)
	}
	if growth := runtime.NumGoroutine() - baseline; growth > limit+20 {
		t.Fatalf("goroutine growth=%d exceeds connection ceiling allowance %d", growth, limit+20)
	}

	for index, conn := range clients {
		_ = conn.Close()
		clients[index] = nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not unblock listener Serve")
	}
	waitFor(t, time.Second, func() bool { return listener.Active() == 0 }, "shutdown lease convergence")
}

func TestConnectionAdmissionOverflowBackoffIsBoundedAndCloseInterrupts(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := newBoundedListener(raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	firstClient, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer firstClient.Close()
	admitted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer admitted.Close()

	acceptDone := make(chan error, 1)
	go func() {
		_, acceptErr := listener.Accept()
		acceptDone <- acceptErr
	}()
	clients := make([]net.Conn, 0, 16)
	for index := 0; index < 16; index++ {
		conn, dialErr := net.Dial("tcp", raw.Addr().String())
		if dialErr != nil {
			break
		}
		clients = append(clients, conn)
	}
	defer func() {
		for _, conn := range clients {
			_ = conn.Close()
		}
	}()
	waitFor(t, time.Second, func() bool { return listener.Rejected() >= 2 }, "overflow rejection backoff")
	before := listener.Rejected()
	time.Sleep(25 * time.Millisecond)
	if additional := listener.Rejected() - before; additional > 7 {
		t.Fatalf("overflow loop spun: %d rejections in 25ms", additional)
	}
	started := time.Now()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-acceptDone:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept after close error=%v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("listener close did not interrupt overflow backoff/Accept")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("close unblock took %s", elapsed)
	}
}

type scriptedListener struct {
	mu      sync.Mutex
	steps   []scriptedAccept
	closed  chan struct{}
	closes  atomic.Int32
	address net.Addr
}

type scriptedAccept struct {
	conn net.Conn
	err  error
}

func (listener *scriptedListener) Accept() (net.Conn, error) {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if len(listener.steps) == 0 {
		<-listener.closed
		return nil, net.ErrClosed
	}
	step := listener.steps[0]
	listener.steps = listener.steps[1:]
	return step.conn, step.err
}
func (listener *scriptedListener) Close() error {
	if listener.closes.Add(1) == 1 {
		close(listener.closed)
	}
	return nil
}
func (listener *scriptedListener) Addr() net.Addr { return listener.address }

func TestConnectionAdmissionAcceptErrorsRepeatedCloseAndExactOnceLease(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	sentinel := errors.New("accept failed")
	raw := &scriptedListener{steps: []scriptedAccept{{err: sentinel}, {conn: left}}, closed: make(chan struct{}), address: testAddr("scripted")}
	listener, err := newBoundedListener(raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := listener.Accept(); !errors.Is(err, sentinel) {
		t.Fatalf("Accept error=%v", err)
	}
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if listener.Active() != 1 {
		t.Fatalf("active=%d", listener.Active())
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if listener.Active() != 0 {
		t.Fatalf("repeated conn Close released lease incorrectly: active=%d", listener.Active())
	}
	_ = listener.Close()
	_ = listener.Close()
	if raw.closes.Load() != 1 {
		t.Fatalf("underlying listener closes=%d", raw.closes.Load())
	}
}

type panicCloseConn struct{ net.Conn }

func (panicCloseConn) Close() error { panic("close canary") }

func TestConnectionLeaseReleasesWhenUnderlyingClosePanics(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	raw := &scriptedListener{
		steps:   []scriptedAccept{{conn: panicCloseConn{Conn: left}}},
		closed:  make(chan struct{}),
		address: testAddr("panic-close"),
	}
	listener, err := newBoundedListener(raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recovered := recover(); recovered != "close canary" {
				t.Fatalf("Close panic=%v", recovered)
			}
		}()
		_ = conn.Close()
	}()
	if active := listener.Active(); active != 0 {
		t.Fatalf("panic leaked connection lease: active=%d", active)
	}
}

type testAddr string

func (address testAddr) Network() string { return "test" }
func (address testAddr) String() string  { return string(address) }
