package main

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestCallHelperHalfClosesRequestDirection(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "helper.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("VECTOR_HELPER_SOCKET", socket)

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		handleHelperConnection(conn)
	}()

	result := make(chan error, 1)
	go func() {
		resp, callErr := callHelper(helperRequest{Action: "diagnostic", Domain: "example.com"})
		if callErr != nil {
			result <- callErr
			return
		}
		if resp.Error != "unsupported helper action" {
			result <- &unexpectedHelperResponse{got: resp.Error}
			return
		}
		result <- nil
	}()

	select {
	case callErr := <-result:
		if callErr != nil {
			t.Fatal(callErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("helper protocol deadlocked waiting for EOF")
	}

	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("helper server did not finish")
	}
}

type unexpectedHelperResponse struct{ got string }

func (e *unexpectedHelperResponse) Error() string {
	return "unexpected helper response: " + e.got
}

func TestHelperTimeoutOrdering(t *testing.T) {
	if helperRoundTripTimeout <= certbotOperationTimeout {
		t.Fatalf("helper round-trip timeout %s must exceed certbot timeout %s", helperRoundTripTimeout, certbotOperationTimeout)
	}
}
