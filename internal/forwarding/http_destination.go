package forwarding

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
)

var destinationDNSHostPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

func validateRemoteDestinationHost(host string) error {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	ip := net.ParseIP(host)
	if host == "" || (ip != nil && ip.IsLoopback()) || (ip == nil &&
		(!destinationDNSHostPattern.MatchString(host) || host == "localhost" || strings.HasSuffix(host, ".localhost"))) {
		return errors.New("destination must use a non-loopback DNS name or IP address")
	}
	if ip == nil {
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return errors.New("destination hostname is invalid")
			}
		}
	}
	return nil
}

func validateDestinationPort(portText string) error {
	if portText == "" {
		return nil
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("destination port must be in the range 1..65535")
	}
	return nil
}
