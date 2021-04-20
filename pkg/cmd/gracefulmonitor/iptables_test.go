package gracefulmonitor

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/coreos/go-iptables/iptables"
)

type testServer struct {
	name    string
	address string
	port    int
}

func (s *testServer) readyMsg() string {
	return fmt.Sprintf("%s-ok", s.name)
}

func (s *testServer) serve(t *testing.T) func() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.address = listener.Addr().String()
	rawPort := s.address[strings.LastIndex(s.address, ":")+1:]
	s.port, err = strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s listening on port %d", s.name, s.port)

	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(s.readyMsg()))
	})
	srv := http.Server{
		Handler: mux,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Errorf("Serve failed: %v", err)
		}
	}()
	return func() {
		err := srv.Close()
		if err != nil {
			t.Errorf("server close failed: %v", err)
		}
	}
}

// Valid simple forwarding semantics (requires sudo and iptables)
func TestForwardToActive(t *testing.T) {
	if os.Getenv("TEST_IPTABLES") == "" {
		t.Skip("Skipping iptables testing due to TEST_IPTABLES not being set")
	}
	// Start first server
	server1 := testServer{name: "one"}
	cleanup1 := server1.serve(t)
	defer cleanup1()

	// Pick a random port to forward
	forwardListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	forwardingAddress := forwardListener.Addr().String()
	rawForwardingPort := forwardingAddress[strings.LastIndex(forwardingAddress, ":")+1:]
	forwardingPort, err := strconv.Atoi(rawForwardingPort)
	t.Logf("forwarding port is %d", forwardingPort)
	defer func() {
		// Keep port open to minimize the potential for interacting
		// with a non-test process.
		err = forwardListener.Close()
		if err != nil {
			t.Errorf("error closing forwarding listener: %v", err)
		}
	}()

	ipt, err := iptables.New()
	if err != nil {
		t.Fatal(err)
	}

	// Enable port forwarding for server
	portMap := map[int]int{
		forwardingPort: server1.port,
	}
	err = ensureActiveRules(ipt, portMap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := removeChain(ipt)
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Check that forwarding works
	readyzURL := fmt.Sprintf("http://%s/readyz", forwardingAddress)
	_, err = checkURL(readyzURL)
	if err != nil {
		t.Fatal(err)
	}

	// Start a second server
	server2 := testServer{name: "two"}
	cleanup2 := server2.serve(t)
	defer cleanup2()

	// Switch the
	nextMap := map[int]int{
		forwardingPort: server2.port,
	}
	err = ensureActiveRules(ipt, nextMap)
	if err != nil {
		t.Fatal(err)
	}

	// Check that the port is now forwarding to the second server.
	forwardedMsg, err := checkURL(readyzURL)
	if err != nil {
		t.Fatal(err)
	}
	if forwardedMsg != server2.readyMsg() {
		t.Fatalf("expected %s, got %s", server2.readyMsg(), forwardedMsg)
	}
}
