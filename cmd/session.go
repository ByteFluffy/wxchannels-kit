package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"wx_channel/internal/application"
	"wx_channel/internal/services"
	"wx_channel/pkg/system"
)

type sessionRecord struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Started    string `json:"started"`
	Nonce      string `json:"nonce"`
}

func newStartCommand() *cobra.Command {
	var manual bool
	command := &cobra.Command{Use: "start", Short: "Run the local service in the foreground; capture requires explicit consent", Args: cobra.NoArgs, RunE: func(command *cobra.Command, args []string) error {
		if start_transferred {
			return nil
		}
		if runtime.GOOS != "darwin" {
			return errors.New("this personal-use distribution currently supports macOS only")
		}
		if capture_api_only {
			apply_server_config_defaults()
		} else {
			if !Cfg.CaptureAuthorized {
				return errors.New("capture was not authorized")
			}
			if Cfg.GetBool("proxy.tun") || Cfg.GetBool("proxy.tcpRelay.enabled") {
				return errors.New("TUN/TCP relay capture is outside the macOS personal-use scope")
			}
			Cfg.Update("proxy.enabled", true)
			Cfg.Update("proxy.system", !manual)
			Cfg.Update("proxy.skipInstallRootCert", false)
			if !manual {
				current, err := system.FetchCurProxy(system.ProxySettings{Device: Cfg.GetString("proxy.networkService")})
				if err != nil {
					return errors.New("cannot inspect the current system proxy; capture was not started")
				}
				if current != nil {
					return errors.New("another system proxy is active; wxc will not overwrite it. Disable it yourself or use --manual-proxy with explicit forwarding")
				}
			}
			if err := services.PrepareLocalCertificate(Cfg); err != nil {
				return err
			}
		}
		return runPersonalSession()
	}}
	command.Flags().BoolVar(&capture_consent, "accept-system-changes", false, "Authorize this capture process to trust its local CA and configure the system proxy")
	command.Flags().BoolVar(&capture_api_only, "api-only", false, "Run the local task API only; do not start capture, generate a CA, or alter system settings")
	command.Flags().BoolVar(&manual, "manual-proxy", false, "Start authorized capture without modifying the system proxy; forward traffic yourself")
	return command
}

func sessionPath() string { return filepath.Join(Cfg.WorkDir, "wxc-session.json") }

func processIdentity(pid int) (string, string, error) {
	if pid <= 0 {
		return "", "", errors.New("invalid process ID")
	}
	read := func(field string) (string, error) {
		command := exec.Command("/bin/ps", "-ww", "-p", strconv.Itoa(pid), "-o", field+"=")
		command.Env = append(os.Environ(), "TZ=UTC")
		data, err := command.Output()
		return strings.TrimSpace(string(data)), err
	}
	executable, err := read("comm")
	if err != nil || executable == "" {
		return "", "", errors.New("process no longer exists")
	}
	started, err := read("lstart")
	if err != nil || started == "" {
		return "", "", errors.New("cannot identify process start time")
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return executable, started, nil
}

func runPersonalSession() error {
	executable, started, err := processIdentity(os.Getpid())
	if err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	record := sessionRecord{PID: os.Getpid(), Executable: executable, Started: started, Nonce: hex.EncodeToString(nonce[:])}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	path := sessionPath()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("a session record already exists or cannot be created; inspect/stop the previous wxc session before starting another")
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	defer removeMatchingSession(path, record.Nonce)
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return application.Start(Cfg)
}

func readPersonalSession(path string) (sessionRecord, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return sessionRecord{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 || info.Mode().Perm()&0077 != 0 {
		return sessionRecord{}, errors.New("session record is not a private regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionRecord{}, err
	}
	var record sessionRecord
	if err := json.Unmarshal(data, &record); err != nil || record.PID <= 0 || record.Executable == "" || record.Started == "" || len(record.Nonce) != 32 {
		return sessionRecord{}, errors.New("invalid session record; refusing to signal any process")
	}
	return record, nil
}

func removeMatchingSession(path, nonce string) {
	record, err := readPersonalSession(path)
	if err == nil && record.Nonce == nonce {
		_ = os.Remove(path)
	}
}

func newStopCommand() *cobra.Command {
	return &cobra.Command{Use: "stop", Short: "Stop only the matching wxc session owned by this runtime directory", Args: cobra.NoArgs, RunE: func(command *cobra.Command, args []string) error {
		path := sessionPath()
		record, err := readPersonalSession(path)
		if errors.Is(err, os.ErrNotExist) {
			return printJSON(command, map[string]any{"status": "not_running"})
		}
		if err != nil {
			return err
		}
		executable, started, err := processIdentity(record.PID)
		if err != nil {
			removeMatchingSession(path, record.Nonce)
			return printJSON(command, map[string]any{"status": "stale_record_removed", "warning": "an unclean capture exit may need manual proxy recovery"})
		}
		ours, err := os.Executable()
		if err != nil {
			return err
		}
		if resolved, err := filepath.EvalSymlinks(ours); err == nil {
			ours = resolved
		}
		if executable != record.Executable || started != record.Started || executable != ours || record.PID == os.Getpid() {
			return errors.New("session process identity does not match this executable; no signal was sent")
		}
		process, err := os.FindProcess(record.PID)
		if err != nil {
			return err
		}
		if err := process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("could not signal the matching wxc process: %w", err)
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		timeout := time.NewTimer(15 * time.Second)
		defer timeout.Stop()
		for {
			select {
			case <-command.Context().Done():
				return command.Context().Err()
			case <-timeout.C:
				return errors.New("shutdown is still in progress; no force-kill was attempted")
			case <-ticker.C:
				if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
					return printJSON(command, map[string]any{"status": "stopped", "pid": record.PID})
				}
			}
		}
	}}
}

func init() { root_cmd.AddCommand(newStartCommand(), newStopCommand()) }
