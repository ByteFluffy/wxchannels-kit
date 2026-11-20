// Package integration_test exercises the built product against generated media
// and loopback-only fixtures. It never enables capture or modifies trust/proxy.
package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"wx_channel/internal/mcpserver"
	"wx_channel/internal/mediaverify"
	"wx_channel/pkg/scraper/wxchannels"
)

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func systemFingerprints(t *testing.T) map[string]string {
	t.Helper()
	result := map[string]string{}
	for name, args := range map[string][]string{
		"proxy":       {"/usr/sbin/scutil", "--proxy"},
		"user_trust":  {"/usr/bin/security", "dump-trust-settings"},
		"admin_trust": {"/usr/bin/security", "dump-trust-settings", "-d"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		data, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		cancel()
		if ctx.Err() == context.DeadlineExceeded {
			t.Fatal("read-only system-state check timed out")
		}
		digest := sha256.Sum256(data)
		result[name] = fmt.Sprintf("%v:%x", err, digest)
	}
	return result
}

func TestNativeCLIAndMCPWithEncryptedGeneratedMedia(t *testing.T) {
	binary := os.Getenv("WXC_TEST_BINARY")
	if binary == "" {
		t.Skip("set WXC_TEST_BINARY to an already built native executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	before := systemFingerprints(t)
	// Register first so this check runs after all child/server cleanup.
	t.Cleanup(func() {
		after := systemFingerprints(t)
		for name, value := range before {
			if after[name] != value {
				t.Errorf("system %s state changed during non-capture integration tests", name)
			}
		}
	})
	home, output := t.TempDir(), t.TempDir()
	apiPort, capturePort := freePort(t), freePort(t)
	configuration := fmt.Sprintf("api:\n  hostname: 127.0.0.1\n  port: %d\nproxy:\n  hostname: 127.0.0.1\n  port: %d\n  enabled: false\n  system: false\nchannels:\n  download:\n    cover: false\nworkdir: %q\n", apiPort, capturePort, home)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "WXC_HOME="+home, "WX_CHANNELS_DOWNLOAD_CONFIG_FILEPATH="+filepath.Join(home, "config.yaml"))
	run := func(args ...string) ([]byte, []byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, args...)
		command.Env = environment
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}
	if _, stderr, err := run("start"); err == nil || !bytes.Contains(stderr, []byte("--accept-system-changes")) {
		t.Fatalf("capture did not refuse missing consent: %s / %v", stderr, err)
	}
	if _, err := os.Stat(filepath.Join(home, "certs")); !os.IsNotExist(err) {
		t.Fatal("consent refusal created a CA directory")
	}
	if _, _, err := run("--help"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(); err != nil {
		t.Fatalf("root should show help, not start capture: %v", err)
	}

	plainPath := filepath.Join(t.TempDir(), "generated.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	generated, err := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=64x48:rate=10", "-t", "0.4", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-movflags", "+faststart", plainPath).CombinedOutput()
	cancel()
	if err != nil {
		t.Fatalf("generate fixture: %s / %v", generated, err)
	}
	plaintext, err := os.ReadFile(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	expectedHash := sha256.Sum256(plaintext)
	// The fixture key is synthetic and public. Reuse the existing XOR stream
	// implementation to synthesize encrypted transport bytes; production
	// download/decryption must recover the exact original hash.
	ciphertext, err := io.ReadAll(wxchannels.NewDecryptReader(bytes.NewReader(plaintext), 123456789, 0, 131072))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(plaintext, ciphertext) {
		t.Fatal("fixture was not encrypted")
	}
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "synthetic.mp4", time.Unix(1, 0), bytes.NewReader(ciphertext))
	}))
	t.Cleanup(media.Close)

	logFile, err := os.CreateTemp(t.TempDir(), "daemon-*.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logFile.Close() })
	daemon := exec.Command(binary, "start", "--api-only")
	daemon.Env = environment
	daemon.Stdout, daemon.Stderr = logFile, logFile
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- daemon.Wait() }()
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_ = daemon.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				_ = daemon.Process.Kill()
				<-done
			}
		}
	})
	realAPI := fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
ready:
	for {
		select {
		case err := <-done:
			stopped = true
			data, _ := os.ReadFile(logFile.Name())
			t.Fatalf("API-only daemon exited during startup: %s / %v", data, err)
		case <-deadline.C:
			t.Fatal("API-only daemon was not ready within the startup budget")
		case <-ticker.C:
			response, err := client.Get(realAPI + "/api/channels/status")
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					break ready
				}
			}
		}
	}
	if connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(capturePort)), 100*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatal("API-only mode opened a capture listener")
	}
	if _, err := os.Stat(filepath.Join(home, "certs")); !os.IsNotExist(err) {
		t.Fatal("API-only mode generated CA files")
	}
	response, err := client.Post(realAPI+"/api/proxy/system/enable", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("administrative API bypassed consent: %d", response.StatusCode)
	}

	apiURL, _ := url.Parse(realAPI)
	forward := httputil.NewSingleHostReverseProxy(apiURL)
	// Only the WeChat page/profile RPC is simulated. Task creation, SQLite,
	// transfer, range handling, decryption, postprocessing, and status are the
	// actual compiled application, not a second downloader or fake task list.
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch r.URL.Path {
		case "/api/channels/status":
			data = map[string]any{"available": true}
		case "/api/channels/feed/profile":
			data = map[string]any{"errCode": 0, "data": map[string]any{"object": map[string]any{
				"id": "synthetic-integration-video", "objectNonceId": "synthetic-nonce", "createtime": 1, "source_url": "https://weixin.qq.com/sph/fixture",
				"contact": map[string]any{"username": "synthetic-fixture", "nickname": "Generated fixture"},
				"objectDesc": map[string]any{"description": "Generated integration fixture", "mediaType": 4, "media": []any{map[string]any{
					"url": media.URL + "/media.mp4", "decodeKey": "123456789", "fileSize": len(ciphertext), "videoPlayLen": 999, "width": 1920, "height": 1080,
					"spec": []any{map[string]any{"fileFormat": "fixture", "width": 1920, "height": 1080}},
				}}},
			}}}
		default:
			forward.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	t.Cleanup(front.Close)

	stdout, stderr, err := run("doctor", "--api-base-url", front.URL)
	if err != nil {
		t.Fatalf("doctor against controlled connection: %s / %s / %v", stdout, stderr, err)
	}
	stdout, stderr, err = run("download", "https://weixin.qq.com/sph/fixture", "--api-base-url", front.URL, "--output", output, "--filename", "generated", "--spec", "fixture", "--timeout", "30s")
	if err != nil {
		data, _ := os.ReadFile(logFile.Name())
		t.Fatalf("native download: %s / %s / daemon %s / %v", stdout, stderr, data, err)
	}
	var result mcpserver.VideoDownloadResult
	if err := json.Unmarshal(stdout, &result); err != nil {
		t.Fatalf("CLI output is not clean JSON: %s / %v", stdout, err)
	}
	if result.Status != "verified" || result.TaskID <= 0 || len(result.Verified) != 1 {
		t.Fatalf("unexpected CLI result: %+v", result)
	}
	report := result.Verified[0]
	if report.Width != 64 || report.Height != 48 || report.Duration < 0.3 || report.Duration > 0.6 || report.SHA256 != hex.EncodeToString(expectedHash[:]) || report.Validation != "decode" {
		t.Fatalf("did not measure/decrypt the actual video: %+v", report)
	}
	canonicalOutput, err := filepath.EvalSymlinks(output)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(report.Path) != canonicalOutput {
		t.Fatalf("native engine did not honor requested output: %s", report.Path)
	}
	stdout, stderr, err = run("task", strconv.Itoa(result.TaskID), "--api-base-url", realAPI)
	if err != nil || bytes.Contains(stdout, []byte("decodeKey")) || bytes.Contains(stdout, []byte(media.URL)) {
		t.Fatalf("task projection failed or leaked transport data: %s / %s / %v", stdout, stderr, err)
	}
	stdout, stderr, err = run("verify", report.Path, "--decode")
	if err != nil {
		t.Fatalf("offline local verification: %s / %v", stderr, err)
	}
	var independent mediaverify.Report
	if err := json.Unmarshal(stdout, &independent); err != nil || independent.SHA256 != report.SHA256 {
		t.Fatalf("independent verify mismatch: %s / %v", stdout, err)
	}
	// Reject actual corrupt/fragmented output through the shipped verifier,
	// not merely through a fake ffprobe response.
	for name, data := range map[string][]byte{
		"not-media.mp4": []byte("synthetic invalid media"),
		"truncated.mp4": plaintext[:len(plaintext)/2],
	} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		stdout, _, err := run("verify", path, "--decode")
		if err == nil || len(bytes.TrimSpace(stdout)) != 0 {
			t.Fatalf("accepted invalid output or printed successful JSON: %s / %s / %v", name, stdout, err)
		}
	}
	stdout, stderr, err = run("download", "https://weixin.qq.com/sph/fixture", "--api-base-url", front.URL, "--output", output, "--existing", "skip", "--timeout", "30s")
	if err != nil {
		t.Fatalf("native duplicate skip: %s / %v", stderr, err)
	}
	var skipped mcpserver.VideoDownloadResult
	if err := json.Unmarshal(stdout, &skipped); err != nil || !skipped.Skipped || skipped.TaskID != result.TaskID || skipped.Status != "verified" {
		t.Fatalf("duplicate was not verified: %s / %v", stdout, err)
	}

	mcpContext, mcpCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer mcpCancel()
	mcp := exec.CommandContext(mcpContext, binary, "mcp", "--api-base-url", front.URL)
	mcp.Env = environment
	input, err := mcp.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	mcpOutput, err := mcp.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var mcpErrors bytes.Buffer
	mcp.Stderr = &mcpErrors
	if err := mcp.Start(); err != nil {
		t.Fatal(err)
	}
	mcpWaited := false
	t.Cleanup(func() {
		_ = input.Close()
		if !mcpWaited {
			_ = mcp.Process.Kill()
			_ = mcp.Wait()
		}
	})
	encoder, decoder := json.NewEncoder(input), json.NewDecoder(mcpOutput)
	call := func(id int, method string, params any) map[string]any {
		t.Helper()
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		var reply map[string]any
		if err := decoder.Decode(&reply); err != nil {
			t.Fatalf("MCP stdout was not protocol-only: %v", err)
		}
		if reply["id"] != float64(id) || reply["error"] != nil {
			t.Fatalf("wrong MCP response: %+v", reply)
		}
		return reply
	}
	call(1, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	catalog := call(2, "tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any)
	if len(catalog) != 4 {
		t.Fatalf("unexpected personal MCP catalog: %d", len(catalog))
	}
	mcpResult := call(3, "tools/call", map[string]any{"name": "download_video", "arguments": map[string]any{"url": "https://weixin.qq.com/sph/fixture", "download_dir": output, "existing_action": "skip", "decode": true, "timeout_seconds": 25}})["result"].(map[string]any)
	if mcpResult["isError"] != false || mcpResult["structuredContent"].(map[string]any)["status"] != "verified" {
		t.Fatalf("MCP shared workflow failed: %+v", mcpResult)
	}
	// Keep stdin open through response ID 3, then close for deterministic shutdown.
	_ = input.Close()
	err = mcp.Wait()
	mcpWaited = true
	if err != nil {
		t.Fatalf("MCP shutdown: %s / %v", mcpErrors.Bytes(), err)
	}

	stdout, stderr, err = run("stop")
	if err != nil {
		t.Fatalf("identity-checked stop: %s / %s / %v", stdout, stderr, err)
	}
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("API-only process did not exit")
	}
	if _, err := os.Stat(filepath.Join(home, "wxc-session.json")); !os.IsNotExist(err) {
		t.Fatal("owned session record was not cleaned")
	}
	if _, err := os.Stat(filepath.Join(home, "certs")); !os.IsNotExist(err) {
		t.Fatal("safe integration tests generated CA material")
	}
	t.Logf("native CLI + MCP passed: task=%d, actual=%dx%d, duration=%.3fs, SHA-256=%s, full decode; proxy/trust unchanged checked during cleanup", result.TaskID, report.Width, report.Height, report.Duration, report.SHA256)
}
