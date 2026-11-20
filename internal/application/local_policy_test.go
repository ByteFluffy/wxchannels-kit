package application

import (
	"testing"

	"github.com/spf13/viper"

	"wx_channel/internal/config"
)

func TestLocalPolicyRejectsCaptureWithoutProcessConsentBeforeStartup(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	cfg := &config.Config{}
	cfg.Update("api.hostname", "127.0.0.1")
	cfg.Update("proxy.hostname", "127.0.0.1")
	cfg.Update("api.port", 2022)
	cfg.Update("proxy.port", 2023)
	cfg.Update("proxy.enabled", true)
	if err := validateLocalStart(cfg); err == nil {
		t.Fatal("config granted capture permission")
	}
	// Exercise the actual public startup entry too: this must return before a
	// logger/database/listener is needed, not merely fail later by accident.
	if err := Start(cfg); err == nil {
		t.Fatal("started unauthorized capture")
	}
	cfg.Update("proxy.enabled", false)
	if err := validateLocalStart(cfg); err != nil {
		t.Fatalf("API-only mode rejected: %v", err)
	}
	cfg.Update("api.hostname", "0.0.0.0")
	if err := validateLocalStart(cfg); err == nil {
		t.Fatal("accepted a remotely exposed control plane")
	}
}
