package services

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"

	"wx_channel/internal/config"
)

func localCertConfig(t *testing.T) *config.Config {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	root := t.TempDir()
	return &config.Config{RootDir: root, WorkDir: root, FullPath: filepath.Join(root, "config.yaml"), Version: "test", Mode: "release"}
}

func TestLocalCertificateRequiresConsentBeforeCreatingFiles(t *testing.T) {
	cfg := localCertConfig(t)
	if err := PrepareLocalCertificate(cfg); err == nil {
		t.Fatal("prepared a CA without consent")
	}
	if _, err := os.Stat(filepath.Join(cfg.WorkDir, "certs")); !os.IsNotExist(err) {
		t.Fatalf("CA directory was touched: %v", err)
	}
}

func TestLocalCertificateIsPrivateValidAndReused(t *testing.T) {
	cfg := localCertConfig(t)
	cfg.CaptureAuthorized = true
	if err := PrepareLocalCertificate(cfg); err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := cfg.GetString("cert.file"), cfg.GetString("cert.key")
	if certPath == "" || keyPath == "" {
		t.Fatal("CA configuration not saved")
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{certPath, keyPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("unsafe CA file mode: %s %v", path, info.Mode())
		}
	}
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareLocalCertificate(cfg); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || cfg.GetString("cert.key") != keyPath {
		t.Fatal("regenerated an existing valid CA")
	}
	info := LoadCertFilesWithInfo()
	if info.Source != CertSourceGenerated || info.IsLegacy {
		t.Fatalf("wrong certificate source: %+v", info)
	}
}

func TestUnconfiguredCertificateDoesNotUseSharedOrOtherAppMaterial(t *testing.T) {
	_ = localCertConfig(t)
	info := LoadCertFilesWithInfo()
	if info.Source != CertSourceUnconfigured || info.IsLegacy || len(info.Cert.Cert) != 0 || len(info.Cert.PrivateKey) != 0 {
		t.Fatal("fell back to CA material without configuration")
	}
	if len(ScanAvailableCerts()) != 0 {
		t.Fatal("offered another application's CA")
	}
	status := NewCertificateService(nil).Status()
	if status["source"] != "unconfigured" || status["installed"] != false || status["trusted"] != false {
		t.Fatalf("wrong unconfigured status: %+v", status)
	}
}
