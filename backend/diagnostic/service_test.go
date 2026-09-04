package diagnostic

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func runGreetingServer(t *testing.T, payload []byte) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		if len(payload) > 0 {
			_, _ = conn.Write(payload)
		}
	}()
	return listener.Addr().String()
}

func TestServiceCheckerDoesNotTreatConnectionArbitraryBytesOrEOFAsHealthy(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload []byte
	}{
		{name: "arbitrary bytes", payload: []byte("HELLO\r\n")},
		{name: "immediate EOF"},
	} {
		t.Run(test.name, func(t *testing.T) {
			address := runGreetingServer(t, test.payload)
			result := (ServiceChecker{ServiceKind: KindSSH, DefaultPort: 22}).Check(context.Background(), Target{Kind: KindSSH, Address: address})
			if result.Status != StatusDegraded || result.ErrorCode != "service_greeting_unverified" {
				t.Fatalf("connection-only behavior remained healthy: %+v", result)
			}
		})
	}
}

func TestServiceCheckerVerifiesSSHGreeting(t *testing.T) {
	address := runGreetingServer(t, []byte("notice one\r\nnotice two\r\nSSH-2.0-OpenSSH_9.8\r\n"))
	result := (ServiceChecker{ServiceKind: KindSSH, DefaultPort: 22}).Check(context.Background(), Target{Kind: KindSSH, Address: address})
	if result.Status != StatusHealthy || result.ErrorCode != "" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Details) != 1 || result.Details["verification_scope"] != "server_greeting" {
		t.Fatalf("details = %#v", result.Details)
	}
}

func TestServiceCheckerVerifiesSMTPGreeting(t *testing.T) {
	for _, kind := range []Kind{KindSMTP, KindSubmission} {
		t.Run(string(kind), func(t *testing.T) {
			address := runGreetingServer(t, []byte("220-mail.example ready\r\n220-PIPELINING\r\n220 final\r\n"))
			result := (ServiceChecker{ServiceKind: kind, DefaultPort: 25}).Check(context.Background(), Target{Kind: kind, Address: address})
			if result.Status != StatusHealthy || result.Details["verification_scope"] != "server_greeting" {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestServiceCheckerVerifiesIMAPGreeting(t *testing.T) {
	for _, greeting := range []string{"* OK ready\r\n", "* PREAUTH ready\r\n"} {
		address := runGreetingServer(t, []byte(greeting))
		result := (ServiceChecker{ServiceKind: KindIMAP, DefaultPort: 143}).Check(context.Background(), Target{Kind: KindIMAP, Address: address})
		if result.Status != StatusHealthy || result.Details["verification_scope"] != "server_greeting" {
			t.Fatalf("greeting %q result = %+v", greeting, result)
		}
	}
}

func TestServiceCheckerVerifiesPOP3Greeting(t *testing.T) {
	for _, greeting := range []string{"+OK ready\r\n", "+OK\r\n"} {
		address := runGreetingServer(t, []byte(greeting))
		result := (ServiceChecker{ServiceKind: KindPOP3, DefaultPort: 110}).Check(context.Background(), Target{Kind: KindPOP3, Address: address})
		if result.Status != StatusHealthy || result.Details["verification_scope"] != "server_greeting" {
			t.Fatalf("greeting %q result = %+v", greeting, result)
		}
	}
}

func TestTLSServiceGreetingFailureOmitsDetails(t *testing.T) {
	certificateServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := certificateServer.TLS.Certificates[0]
	certificateServer.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		raw, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		secured := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
		defer secured.Close()
		if secured.Handshake() == nil {
			_, _ = secured.Write([]byte("* BYE unavailable\r\n"))
		}
	}()

	result := (ServiceChecker{ServiceKind: KindIMAPS, DefaultPort: 993, UseTLS: true, TLSConfig: &tls.Config{InsecureSkipVerify: true}}).Check(context.Background(), Target{Kind: KindIMAPS, Address: listener.Addr().String()})
	if result.Status != StatusDegraded || result.ErrorCode != ResultErrorServiceGreetingUnverified || len(result.Details) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

type countingReader struct {
	reader io.Reader
	count  int
}

func (reader *countingReader) Read(payload []byte) (int, error) {
	n, err := reader.reader.Read(payload)
	reader.count += n
	return n, err
}

func TestServerGreetingGrammarBoundaries(t *testing.T) {
	continuation512 := "220-" + strings.Repeat("x", 506) + "\r\n"
	final512 := "220 " + strings.Repeat("x", 506) + "\r\n"
	ssh255 := "SSH-2.0-" + strings.Repeat("x", 245) + "\r\n"
	sevenPrelines := strings.Repeat("notice\r\n", 7)
	cases := []struct {
		name    string
		kind    Kind
		payload string
		valid   bool
		read    int
	}{
		{"ssh no prelines", KindSSH, "SSH-2.0-x\r\n", true, 0},
		{"ssh seven prelines", KindSSH, sevenPrelines + ssh255, true, 0},
		{"ssh eight prelines", KindSSH, sevenPrelines + "eighth\r\nSSH-2.0-x\r\n", false, len(sevenPrelines + "eighth\r\n")},
		{"ssh identification 255", KindSSH, ssh255, true, 255},
		{"ssh identification 256", KindSSH, "SSH-2.0-" + strings.Repeat("x", 246) + "\r\n", false, 256},
		{"ssh old version", KindSSH, "SSH-1.5-old\r\n", false, 0},
		{"smtp single", KindSMTP, "220 ready\r\n", true, 0},
		{"smtp multiline", KindSubmission, "220-first\r\n220-second\r\n220 final\r\n", true, 0},
		{"smtp wrong continuation code", KindSMTP, "220-first\r\n221 final\r\n", false, 0},
		{"smtp line 512", KindSMTP, final512, true, 512},
		{"smtp line 513", KindSMTP, "220 " + strings.Repeat("x", 507) + "\r\n", false, 513},
		{"aggregate 4096", KindSMTP, strings.Repeat(continuation512, 7) + final512, true, 4096},
		{"aggregate 4097 unread", KindSMTP, strings.Repeat(continuation512, 8) + "x", false, 4096},
		{"ninth line unread", KindSMTP, strings.Repeat("220-more\r\n", 8) + "220 final\r\n", false, len(strings.Repeat("220-more\r\n", 8))},
		{"imap ok", KindIMAP, "* OK\r\n", true, 0},
		{"imap preauth", KindIMAPS, "* PREAUTH ready\r\n", true, 0},
		{"imap bye", KindIMAP, "* BYE go away\r\n", false, 0},
		{"imap prefix collision", KindIMAP, "* OKAY no\r\n", false, 0},
		{"pop ok", KindPOP3S, "+OK\r\n", true, 0},
		{"pop prefix collision", KindPOP3, "+OKAY no\r\n", false, 0},
		{"bare LF", KindPOP3, "+OK ready\n", false, 0},
		{"missing CRLF", KindPOP3, "+OK ready", false, 0},
		{"wrong family", KindSSH, "+OK ready\r\n", false, 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			reader := &countingReader{reader: strings.NewReader(test.payload)}
			err := verifyServerGreeting(reader, test.kind)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v bytes_read=%d", test.valid, err, reader.count)
			}
			if test.read != 0 && reader.count != test.read {
				t.Fatalf("bytes_read=%d want=%d", reader.count, test.read)
			}
		})
	}
}

func TestServerGreetingReadErrorDoesNotExposeProse(t *testing.T) {
	reader := io.MultiReader(strings.NewReader("+O"), errorReader{err: errors.New("GREETING_READ_CANARY")})
	if err := verifyServerGreeting(reader, KindPOP3); !errors.Is(err, errServiceGreetingUnverified) || strings.Contains(err.Error(), "CANARY") {
		t.Fatalf("err=%v", err)
	}
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

type blockingGreetingConn struct {
	readStarted chan struct{}
	unblock     chan struct{}
	startOnce   sync.Once
	closeOnce   sync.Once
	mu          sync.Mutex
	closes      int
	writes      int
	deadline    time.Time
}

func newBlockingGreetingConn() *blockingGreetingConn {
	return &blockingGreetingConn{readStarted: make(chan struct{}), unblock: make(chan struct{})}
}

func (conn *blockingGreetingConn) Read([]byte) (int, error) {
	conn.startOnce.Do(func() { close(conn.readStarted) })
	<-conn.unblock
	return 0, net.ErrClosed
}
func (conn *blockingGreetingConn) Write(payload []byte) (int, error) {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	conn.writes += len(payload)
	return len(payload), nil
}
func (conn *blockingGreetingConn) Close() error {
	conn.closeOnce.Do(func() {
		conn.mu.Lock()
		conn.closes++
		conn.mu.Unlock()
		close(conn.unblock)
	})
	return nil
}
func (*blockingGreetingConn) LocalAddr() net.Addr  { return stubAddr("local") }
func (*blockingGreetingConn) RemoteAddr() net.Addr { return stubAddr("remote") }
func (conn *blockingGreetingConn) SetDeadline(deadline time.Time) error {
	conn.mu.Lock()
	conn.deadline = deadline
	conn.mu.Unlock()
	return nil
}
func (*blockingGreetingConn) SetReadDeadline(time.Time) error  { return nil }
func (*blockingGreetingConn) SetWriteDeadline(time.Time) error { return nil }

type stubAddr string

func (address stubAddr) Network() string { return "stub" }
func (address stubAddr) String() string  { return string(address) }

type fixedConnDialer struct{ conn net.Conn }

func (dialer fixedConnDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return dialer.conn, nil
}

func TestServiceGreetingDeadlineClosesOnceAndPreservesTimeout(t *testing.T) {
	conn := newBlockingGreetingConn()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	deadline, _ := ctx.Deadline()
	started := time.Now()
	result := (ServiceChecker{ServiceKind: KindPOP3, DefaultPort: 110, Dialer: fixedConnDialer{conn}}).Check(ctx, Target{Kind: KindPOP3, Address: "example.test"})
	if result.ErrorCode != "timeout" || result.Status != StatusUnreachable || time.Since(started) > 200*time.Millisecond {
		t.Fatalf("result=%+v elapsed=%v", result, time.Since(started))
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.closes != 1 || conn.writes != 0 || !conn.deadline.Equal(deadline) {
		t.Fatalf("closes=%d writes=%d deadline=%v want=%v", conn.closes, conn.writes, conn.deadline, deadline)
	}
}

func TestServiceGreetingCancellationWinsReadRace(t *testing.T) {
	for i := 0; i < 20; i++ {
		conn := newBlockingGreetingConn()
		ctx, cancel := context.WithCancel(context.Background())
		resultChannel := make(chan Result, 1)
		go func() {
			resultChannel <- (ServiceChecker{ServiceKind: KindIMAP, DefaultPort: 143, Dialer: fixedConnDialer{conn}}).Check(ctx, Target{Kind: KindIMAP, Address: "example.test"})
		}()
		<-conn.readStarted
		cancel()
		result := <-resultChannel
		if result.ErrorCode != "cancelled" || result.Status != StatusUnreachable {
			t.Fatalf("iteration=%d result=%+v", i, result)
		}
		conn.mu.Lock()
		closes, writes := conn.closes, conn.writes
		conn.mu.Unlock()
		if closes != 1 || writes != 0 {
			t.Fatalf("iteration=%d closes=%d writes=%d", i, closes, writes)
		}
	}
}

func TestServiceGreetingParserDoesNotWrite(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	written := make(chan []byte, 1)
	go func() {
		_, _ = server.Write([]byte("+OK ready\r\n"))
		_ = server.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		payload, _ := io.ReadAll(server)
		written <- payload
	}()
	result := (ServiceChecker{ServiceKind: KindPOP3, DefaultPort: 110, Dialer: fixedConnDialer{client}}).Check(context.Background(), Target{Kind: KindPOP3, Address: "example.test"})
	if result.Status != StatusHealthy || !bytes.Equal(<-written, nil) {
		t.Fatalf("result=%+v client bytes were written", result)
	}
}
