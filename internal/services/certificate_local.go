package services

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wx_channel/internal/config"
	"wx_channel/pkg/certificate"
)

// PrepareLocalCertificate creates no trust entries. It only prepares private
// local files for an explicitly authorized capture startup. The interceptor
// remains the sole owner of certificate installation and proxy startup.
func PrepareLocalCertificate(cfg *config.Config) error {
	if cfg == nil || !cfg.CaptureAuthorized {
		return errors.New("local CA preparation requires explicit capture consent")
	}
	certPath, keyPath := strings.TrimSpace(cfg.GetString("cert.file")), strings.TrimSpace(cfg.GetString("cert.key"))
	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" {
			return errors.New("both cert.file and cert.key must be configured")
		}
		pair, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil || len(pair.Certificate) == 0 {
			return errors.New("configured local CA files cannot be loaded or do not match")
		}
		root, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil || !root.IsCA || time.Now().Before(root.NotBefore) || !time.Now().Before(root.NotAfter) {
			return errors.New("configured CA is invalid or expired; replace it explicitly")
		}
		// Use the actual subject for status checks, not an unrelated default name.
		cfg.Update("cert.name", root.Subject.CommonName)
		return nil
	}
	if strings.TrimSpace(cfg.WorkDir) == "" {
		return errors.New("capture requires a private runtime directory")
	}
	directory, err := filepath.Abs(filepath.Join(cfg.WorkDir, "certs"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("the local certs directory must be private (0700) and not a symlink")
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	name := "wxchannels-kit-" + hex.EncodeToString(nonce[:])
	public, private, err := certificate.GenerateRootCA(name, 365*24*time.Hour)
	if err != nil {
		return err
	}
	certPath, keyPath = filepath.Join(directory, name+".pem"), filepath.Join(directory, name+".key")
	if err := writeNewPrivateFile(certPath, public); err != nil {
		return err
	}
	if err := writeNewPrivateFile(keyPath, private); err != nil {
		return err
	}
	if err := cfg.UpdateAndSave(map[string]interface{}{"cert.file": certPath, "cert.key": keyPath, "cert.name": name}); err != nil {
		return errors.New("local CA was generated but its configuration could not be saved; no trust changes were made")
	}
	return nil
}

func writeNewPrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
