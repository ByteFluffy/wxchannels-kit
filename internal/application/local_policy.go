package application

import (
	"errors"
	"net"
	"strings"

	"wx_channel/internal/config"
)

// validateLocalStart runs before database, listener, CA, or proxy initialization.
// Config values alone cannot grant process-local consent to interception.
func validateLocalStart(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("configuration is required")
	}
	if cfg.GetBool("proxy.enabled") && !cfg.CaptureAuthorized {
		return errors.New("capture is not authorized for this process; use wxc start --accept-system-changes explicitly")
	}
	for _, key := range []string{"api.hostname", "proxy.hostname"} {
		host := strings.Trim(cfg.GetString(key), "[]")
		if host == "localhost" {
			host = "127.0.0.1"
			cfg.Update(key, host)
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("the personal-use API and capture listeners must bind to loopback")
		}
	}
	for _, key := range []string{"api.port", "proxy.port"} {
		if port := cfg.GetInt(key); port < 1 || port > 65535 {
			return errors.New("local service ports must be between 1 and 65535")
		}
	}
	if cfg.GetBool("bridge.enabled") || cfg.GetBool("download.remoteServer.enabled") {
		return errors.New("remote bridge/downloader mode is not enabled in this local distribution")
	}
	return nil
}
