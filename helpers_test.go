package leaderelection

import (
	"net"
	"testing"
)

func TestGetSelfHostPort(t *testing.T) {
	selfPort := "8080"
	hostPorts, hostPortErr := GetSelfHostPort(selfPort)
	if hostPortErr != nil {
		t.Errorf("unexpected error getting self IP address: %s", hostPortErr)
	}

	if len(hostPorts) < 1 {
		t.Error("unexpected 0 length of hostPort slice. Expected at least 1")
	}
	for _, hostPort := range hostPorts {
		host, port, splitErr := net.SplitHostPort(hostPort)
		if splitErr != nil {
			t.Errorf("unexpected error splitting host and port: %s", splitErr)
		}
		if port != selfPort {
			t.Errorf("unexpected port. Expected: %q, got: %q", selfPort, port)
		}
		if host == "127.0.0.1" {
			t.Error("found unexpected localhost address")
		}
	}
}
