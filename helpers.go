package leaderelection

import (
	"fmt"
	"net"
)

// SelfHostPorts gets the global unicast addresses of the local execution
// environment and returns a slice of IP:Port which can be passed to
// Config.HostPort
func SelfHostPorts(port string) ([]string, error) {
	addresses, addrErr := net.InterfaceAddrs()
	if addrErr != nil {
		return nil, fmt.Errorf("Unable to get self IP address: %w", addrErr)
	}

	hostPorts := make([]string, 0, 1)
	for _, addr := range addresses {
		switch v := addr.(type) {
		case *net.IPNet:
			if v.IP != nil && v.IP.IsGlobalUnicast() {
				hostPort := net.JoinHostPort(v.IP.String(), port)
				hostPorts = append(hostPorts, hostPort)
			}
		}
	}

	return hostPorts, nil
}
