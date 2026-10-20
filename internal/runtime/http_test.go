package runtime

import "testing"

func TestIsLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:4318", "[::1]:4318"} {
		if !IsLoopbackAddress(address) {
			t.Errorf("IsLoopbackAddress(%q) = false", address)
		}
	}
	for _, address := range []string{"0.0.0.0:4318", "192.168.1.2:4318", "localhost:4318", ":4318", "127.0.0.1"} {
		if IsLoopbackAddress(address) {
			t.Errorf("IsLoopbackAddress(%q) = true", address)
		}
	}
}
