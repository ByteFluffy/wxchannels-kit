package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wx_channel/internal/mediaverify"
)

type fakeVideoVerifier struct {
	calls atomic.Int32
	fail  bool
}

func (f *fakeVideoVerifier) CheckTools(bool) error { return nil }
func (f *fakeVideoVerifier) Verify(ctx context.Context, path string, decode bool) (mediaverify.Report, error) {
	f.calls.Add(1)
	if f.fail {
		return mediaverify.Report{}, errors.New("https://example.invalid/?secret=DO_NOT_PRINT")
	}
	info, err := os.Stat(path)
	if err != nil {
		return mediaverify.Report{}, err
	}
	return mediaverify.Report{Path: path, Size: info.Size(), Width: 1280, Height: 720, Duration: 1, Validation: map[bool]string{false: "probe", true: "decode"}[decode]}, nil
}

type videoFixture struct {
	path             string
	status           int
	firstPending     bool
	skipped          bool
	withoutListFiles bool
	submissions      atomic.Int32
	polls            atomic.Int32
	created          chan DownloadTaskCreateRequest
}

func (f *videoFixture) task() map[string]any {
	return map[string]any{"id": 17, "name": "synthetic fixture", "status": f.status, "error": "https://cdn.invalid/video?decode_key=DO_NOT_PRINT", "files": []any{map[string]any{"file_path": f.path, "name": filepath.Base(f.path), "type": "video", "size": 7, "url": "https://cdn.invalid/video?token=DO_NOT_PRINT"}}}
}
func (f *videoFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var data any
	switch r.URL.Path {
	case "/api/channels/status":
		data = map[string]any{"available": true}
	case "/api/channels/feed/profile":
		if r.URL.Query().Get("url") != "https://weixin.qq.com/sph/fixture" {
			http.Error(w, "wrong URL", 400)
			return
		}
		data = map[string]any{"errCode": 0, "data": map[string]any{"object": map[string]any{"id": "fixture"}}}
	case "/api/v1/download_task/create":
		f.submissions.Add(1)
		var body struct {
			Objects []DownloadTaskCreateRequest `json:"objects"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Objects) != 1 {
			http.Error(w, "bad request", 400)
			return
		}
		if f.created != nil {
			f.created <- body.Objects[0]
		}
		data = map[string]any{"tasks": []any{map[string]any{"code": 0, "data": map[string]any{"id": 17, "skipped": f.skipped}}}, "ids": []int{17}}
	case "/api/v1/download_task/list":
		task := f.task()
		if f.firstPending && f.polls.Add(1) == 1 {
			task["status"] = 2
		}
		if f.withoutListFiles {
			delete(task, "files")
		}
		data = map[string]any{"list": []any{task}}
	case "/api/v1/download_task/detail":
		data = f.task()
	default:
		http.Error(w, "unexpected endpoint", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
}

func newVideoFixture(t *testing.T, status int) (*videoFixture, *Server, *fakeVideoVerifier, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "video.mp4")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := &videoFixture{path: path, status: status}
	backend := httptest.NewServer(fixture)
	t.Cleanup(backend.Close)
	verifier := &fakeVideoVerifier{}
	server, err := NewServer(Config{APIBaseURL: backend.URL, MediaVerifier: verifier, PollInterval: time.Millisecond, AllowedTools: PersonalToolNames()})
	if err != nil {
		t.Fatal(err)
	}
	return fixture, server, verifier, root
}

func TestNativeDownloadReusesTaskCreationAndWaitsForRealOutput(t *testing.T) {
	fixture, server, verifier, root := newVideoFixture(t, 5)
	fixture.firstPending = true
	fixture.created = make(chan DownloadTaskCreateRequest, 1)
	result, err := server.DownloadVideo(context.Background(), VideoDownloadRequest{URL: "https://weixin.qq.com/sph/fixture?tracking=discard", DownloadDir: root, Filename: "mine", Spec: "xWT111", Decode: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != 17 || result.Status != "verified" || len(result.Verified) != 1 || verifier.calls.Load() != 1 || fixture.submissions.Load() != 1 || fixture.polls.Load() < 2 {
		t.Fatalf("wrong result/counters: %+v", result)
	}
	request := <-fixture.created
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if request.DownloadDir != canonicalRoot || request.Filename != "mine" || request.Config["video_variant_spec"] != "xWT111" || request.Platform != "wxchannels" || request.AutoStart == nil || !*request.AutoStart {
		t.Fatalf("wrong native request: %+v", request)
	}
}

func TestNativeWorkflowTerminalFailuresDoNotVerifyOrLeakCredentials(t *testing.T) {
	for _, status := range []int{6, 7} {
		t.Run(map[int]string{6: "failed", 7: "cancelled"}[status], func(t *testing.T) {
			fixture, server, verifier, root := newVideoFixture(t, status)
			result, err := server.DownloadVideo(context.Background(), VideoDownloadRequest{URL: "https://weixin.qq.com/sph/fixture", DownloadDir: root})
			if err == nil || result == nil || result.TaskID != 17 || verifier.calls.Load() != 0 || fixture.submissions.Load() != 1 {
				t.Fatalf("unexpected result: %+v / %v", result, err)
			}
			encoded, _ := json.Marshal(tool_error_result(err))
			if strings.Contains(string(encoded), "DO_NOT_PRINT") || strings.Contains(string(encoded), "cdn.invalid") {
				t.Fatalf("leaked backend data: %s", encoded)
			}
		})
	}
}

func TestNativeWaitTimeoutDoesNotResubmitOrClaimTaskCancellation(t *testing.T) {
	fixture, server, verifier, root := newVideoFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := server.DownloadVideo(ctx, VideoDownloadRequest{URL: "https://weixin.qq.com/sph/fixture", DownloadDir: root})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || result == nil || result.TaskID != 17 || verifier.calls.Load() != 0 || fixture.submissions.Load() != 1 || !strings.Contains(err.Error(), "may still be running") {
		t.Fatalf("wrong timeout semantics: %+v / %v", result, err)
	}
}

func TestNativeWorkflowFetchesDetailWhenListOmitsFiles(t *testing.T) {
	fixture, server, _, root := newVideoFixture(t, 5)
	fixture.withoutListFiles = true
	if _, err := server.DownloadVideo(context.Background(), VideoDownloadRequest{URL: "https://weixin.qq.com/sph/fixture", DownloadDir: root}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeWorkflowVerifiesSkippedExistingTask(t *testing.T) {
	fixture, server, verifier, root := newVideoFixture(t, 5)
	fixture.skipped = true
	result, err := server.DownloadVideo(context.Background(), VideoDownloadRequest{URL: "https://weixin.qq.com/sph/fixture", DownloadDir: root, ExistingAction: "skip"})
	if err != nil || !result.Skipped || verifier.calls.Load() != 1 || fixture.submissions.Load() != 1 {
		t.Fatalf("skip was not verified: %+v / %v", result, err)
	}
}

func TestNativeWorkflowRejectsEscapedOrMissingFileBeforeProbe(t *testing.T) {
	for _, mode := range []string{"outside", "symlink", "missing"} {
		t.Run(mode, func(t *testing.T) {
			fixture, server, verifier, root := newVideoFixture(t, 5)
			outside := filepath.Join(t.TempDir(), "outside.mp4")
			if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "outside":
				fixture.path = outside
			case "symlink":
				link := filepath.Join(root, "link.mp4")
				if err := os.Symlink(outside, link); err != nil {
					t.Fatal(err)
				}
				fixture.path = link
			case "missing":
				fixture.path = filepath.Join(root, "missing.mp4")
			}
			if _, err := server.DownloadVideo(context.Background(), VideoDownloadRequest{URL: "https://weixin.qq.com/sph/fixture", DownloadDir: root}); err == nil || verifier.calls.Load() != 0 {
				t.Fatalf("accepted unsafe output: %v", err)
			}
		})
	}
}

func TestNativeWorkflowRejectsInvalidInputBeforeAnySubmission(t *testing.T) {
	for _, request := range []VideoDownloadRequest{
		{URL: "https://example.invalid/video"},
		{URL: "https://weixin.qq.com/sph/fixture", Filename: "../escape"},
		{URL: "https://weixin.qq.com/sph/fixture", Filename: "NUL.mp4"},
		{URL: "https://weixin.qq.com/sph/fixture", ExistingAction: "try-again"},
		{URL: "https://weixin.qq.com/sph/fixture", Spec: "https://example.invalid"},
		{URL: "https://weixin.qq.com/sph/fixture", TimeoutSeconds: -1},
	} {
		fixture, server, _, root := newVideoFixture(t, 5)
		request.DownloadDir = root
		if _, err := server.DownloadVideo(context.Background(), request); err == nil || fixture.submissions.Load() != 0 {
			t.Fatalf("accepted input: %+v / %v", request, err)
		}
	}
}

func TestVideoTaskProjectionOmitsSecrets(t *testing.T) {
	_, server, _, _ := newVideoFixture(t, 5)
	task, err := server.VideoTask(context.Background(), 17)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(task)
	if strings.Contains(string(data), "DO_NOT_PRINT") || strings.Contains(string(data), "cdn.invalid") || task.State != "finished" {
		t.Fatalf("unsafe task projection: %s", data)
	}
}

func TestPersonalToolCatalogDeniesAdministrativeTools(t *testing.T) {
	_, server, _, _ := newVideoFixture(t, 5)
	definitions := server.tool_definitions()
	if len(definitions) != len(PersonalToolNames()) {
		t.Fatalf("unexpected catalog size: %d", len(definitions))
	}
	for _, name := range []string{"update_config", "deploy_sph_worker", "get_logs", "get_config", "decrypt_wxchannels_video"} {
		if _, err := server.call_tool(context.Background(), call_tool_params{Name: name, Arguments: json.RawMessage(`{}`)}); !errors.Is(err, err_unknown_tool) {
			t.Errorf("allowed administrative tool %s", name)
		}
	}
}

func TestMCPKeepsRequestAliveWhileStdinRemainsOpen(t *testing.T) {
	fixture, original, _, root := newVideoFixture(t, 5)
	fixture.firstPending = true
	input, inputWriter := io.Pipe()
	outputReader, output := io.Pipe()
	server, err := NewServer(Config{APIBaseURL: original.api_client.base_url, MediaVerifier: original.media_verifier, PollInterval: time.Millisecond, AllowedTools: PersonalToolNames(), Input: input, Output: output})
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { err := server.Serve(context.Background()); _ = output.Close(); closed <- err }()
	deadline := time.AfterFunc(5*time.Second, func() { _ = inputWriter.Close(); _ = outputReader.Close() })
	defer deadline.Stop()
	defer input.Close()
	defer inputWriter.Close()
	defer outputReader.Close()
	encoder, decoder := json.NewEncoder(inputWriter), json.NewDecoder(outputReader)
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2024-11-05"}}); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response["id"] != float64(1) || response["error"] != nil {
		t.Fatalf("initialize failed: %v", response)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "download_video", "arguments": map[string]any{"url": "https://weixin.qq.com/sph/fixture", "download_dir": root}}}); err != nil {
		t.Fatal(err)
	}
	// Deliberately do not close inputWriter before receiving this response.
	response = nil
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response["id"] != float64(2) || response["error"] != nil {
		t.Fatalf("download RPC failed: %v", response)
	}
	result := response["result"].(map[string]any)
	if result["isError"] != false || result["structuredContent"].(map[string]any)["status"] != "verified" {
		t.Fatalf("download did not finish with stdin open: %v", response)
	}
	_ = inputWriter.Close()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}
