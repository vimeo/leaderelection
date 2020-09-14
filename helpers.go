package leaderelection

import (
	"fmt"
	"net"
)

// GetSelfHostPort gets the global unicast address of where the application is
// running and returns a slice of IP:Port which can be passed to Config.HostPort
func GetSelfHostPort(port string) ([]string, error) {
	addresses, addrErr := net.InterfaceAddrs()
	if addrErr != nil {
		return nil, fmt.Errorf("Unable to get self IP address: %w", addrErr)
	}

	var selfIP []string
	for _, addr := range addresses {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP.To4()
			if ip == nil {
				ip = v.IP.To16()
			}
		}
		if ip != nil && ip.IsGlobalUnicast() {
			hostPort := net.JoinHostPort(ip.String(), port)
			selfIP = append(selfIP, hostPort)
		}
	}

	return selfIP, nil
}
