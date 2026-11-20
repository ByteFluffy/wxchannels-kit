package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"wx_channel/internal/mediaverify"
)

// VideoVerifier is shared by the native CLI and personal-use MCP entry points.
type VideoVerifier interface {
	CheckTools(decode bool) error
	Verify(context.Context, string, bool) (mediaverify.Report, error)
}

type VideoDownloadRequest struct {
	URL            string `json:"url"`
	DownloadDir    string `json:"download_dir"`
	Filename       string `json:"filename,omitempty"`
	Spec           string `json:"spec,omitempty"`
	ExistingAction string `json:"existing_action,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Decode         bool   `json:"decode,omitempty"`
}

type VideoDownloadResult struct {
	TaskID   int                  `json:"task_id"`
	Status   string               `json:"status"`
	Skipped  bool                 `json:"skipped"`
	Verified []mediaverify.Report `json:"files"`
}

// VideoTask is deliberately a safe projection, not a second task store. URL,
// key, cookies, headers, and raw backend errors are never projected here.
type VideoTask struct {
	ID         int     `json:"id"`
	Name       string  `json:"name"`
	Status     int     `json:"status"`
	State      string  `json:"state"`
	Progress   float64 `json:"progress"`
	Size       int64   `json:"size"`
	Downloaded int64   `json:"downloaded"`
	Files      []struct {
		Path       string `json:"file_path"`
		Name       string `json:"name"`
		Type       string `json:"type"`
		Size       int64  `json:"size"`
		Downloaded int64  `json:"downloaded"`
	} `json:"files"`
}

type DownloadError struct {
	TaskID int
	stage  string
	cause  error
}

func (e *DownloadError) Error() string {
	if e.TaskID <= 0 {
		return "video submission failed or timed out; check the local service and existing tasks before submitting again (no automatic retry was made)"
	}
	if errors.Is(e.cause, context.Canceled) || errors.Is(e.cause, context.DeadlineExceeded) {
		return fmt.Sprintf("stopped waiting for task %d; the task may still be running. Check it with wxc task %d", e.TaskID, e.TaskID)
	}
	return fmt.Sprintf("task %d did not pass %s; inspect it with wxc task %d (raw backend errors are withheld)", e.TaskID, e.stage, e.TaskID)
}

// Unwrap exposes cancellation only, never a backend error containing credentials.
func (e *DownloadError) Unwrap() error {
	if errors.Is(e.cause, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(e.cause, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}

func workflowFailure(id int, stage string, err error) error {
	return &DownloadError{TaskID: id, stage: stage, cause: err}
}

func (s *Server) verifier() VideoVerifier {
	if s.media_verifier != nil {
		return s.media_verifier
	}
	return mediaverify.New()
}

// DownloadVideo uses the existing resolver/task creator and the existing task
// waiter. Both the CLI and the download_video MCP tool call this exact method.
// It never starts capture, spawns the original executable, or reconstructs a CDN URL.
func (s *Server) DownloadVideo(ctx context.Context, request VideoDownloadRequest) (*VideoDownloadResult, error) {
	canonical, err := normalizeShareURL(request.URL)
	if err != nil {
		return nil, err
	}
	if err := validatePortableFilename(request.Filename); err != nil {
		return nil, err
	}
	if request.Spec != "" && !portableSpec.MatchString(request.Spec) {
		return nil, errors.New("spec must be an advertised variant identifier, not a URL or path")
	}
	if request.ExistingAction == "" {
		request.ExistingAction = "error"
	}
	if !is_existing_action(request.ExistingAction) {
		return nil, errors.New("existing_action must be error, skip, overwrite, or duplicate")
	}
	duration, err := timeout_duration(request.TimeoutSeconds, 900, 21600)
	if err != nil {
		return nil, err
	}
	root, err := localOutputDir(request.DownloadDir)
	if err != nil {
		return nil, err
	}
	verifier := s.verifier()
	if err := verifier.CheckTools(request.Decode); err != nil {
		return nil, err
	}
	if s.api_client == nil {
		return nil, errors.New("start the local wxc service before downloading")
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, errors.New("cannot create the requested output directory")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("cannot resolve the requested output directory")
	}
	arguments, err := json.Marshal(download_wxchannels_video_arguments{
		URL: canonical, DownloadDir: root, Filename: request.Filename,
		ExistingAction: request.ExistingAction, VideoVariantSpec: request.Spec,
	})
	if err != nil {
		return nil, err
	}
	// Preserve the native path/variant/duplicate semantics instead of making a
	// second task creation implementation in the CLI.
	created, err := s.download_wxchannels_video(ctx, arguments)
	if err != nil {
		return nil, workflowFailure(0, "submission", err)
	}
	var creation struct {
		Skipped bool  `json:"skipped"`
		IDs     []int `json:"ids"`
		Task    struct {
			ID int `json:"id"`
		} `json:"task"`
		Existing struct {
			ID int `json:"id"`
		} `json:"existing_task"`
	}
	if err := decodeStructured(created, &creation); err != nil {
		return nil, workflowFailure(0, "submission", err)
	}
	id := creation.Task.ID
	if creation.Skipped {
		id = creation.Existing.ID
	}
	if id <= 0 && len(creation.IDs) == 1 {
		id = creation.IDs[0]
	}
	if id <= 0 {
		return nil, workflowFailure(0, "submission", errors.New("missing task ID"))
	}
	result := &VideoDownloadResult{TaskID: id, Status: "pending", Skipped: creation.Skipped, Verified: []mediaverify.Report{}}
	task, err := s.wait_download_task(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return result, workflowFailure(id, "completion", err)
	}
	data, err := json.Marshal(task)
	if err != nil {
		return result, workflowFailure(id, "completion", err)
	}
	var snapshot VideoTask
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return result, workflowFailure(id, "completion", err)
	}
	if snapshot.ID != id || snapshot.Status != 5 {
		return result, workflowFailure(id, "completion", errors.New("invalid terminal snapshot"))
	}
	// Some task-list projections omit files. The existing detail tool provides
	// the authoritative file records, without guessing a path from the title.
	if len(snapshot.Files) == 0 {
		snapshotPointer, err := s.VideoTask(ctx, id)
		if err != nil {
			return result, workflowFailure(id, "file lookup", err)
		}
		snapshot = *snapshotPointer
	}
	if snapshot.Status != 5 {
		return result, workflowFailure(id, "completion", errors.New("task changed while looking up output"))
	}
	seen := map[string]bool{}
	for _, file := range snapshot.Files {
		ext := strings.ToLower(filepath.Ext(file.Path))
		if file.Type != "video" && ext != ".mp4" && ext != ".m4v" && ext != ".mov" && ext != ".mkv" && ext != ".webm" && ext != ".ts" {
			continue
		}
		if file.Path == "" {
			return result, workflowFailure(id, "file lookup", errors.New("missing video path"))
		}
		path, err := containedOutput(root, file.Path)
		if err != nil {
			return result, workflowFailure(id, "output containment", err)
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		report, err := verifier.Verify(ctx, path, request.Decode)
		if err != nil {
			return result, workflowFailure(id, "media verification", err)
		}
		result.Verified = append(result.Verified, report)
	}
	if len(result.Verified) == 0 {
		return result, workflowFailure(id, "media verification", errors.New("no completed video file"))
	}
	result.Status = "verified"
	return result, nil
}

func decodeStructured(result map[string]any, destination any) error {
	if result["isError"] == true {
		return errors.New("tool failed")
	}
	value, ok := result["structuredContent"]
	if !ok {
		return errors.New("tool result has no structured content")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, destination)
}

func (s *Server) VideoTask(ctx context.Context, id int) (*VideoTask, error) {
	if id <= 0 {
		return nil, errors.New("task ID must be a positive integer")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	arguments, _ := json.Marshal(map[string]int{"id": id})
	result, err := s.get_download_task_detail(ctx, arguments)
	if err != nil {
		return nil, workflowFailure(id, "status query", err)
	}
	var task VideoTask
	if err := decodeStructured(result, &task); err != nil || task.ID != id {
		return nil, workflowFailure(id, "status query", errors.New("invalid task snapshot"))
	}
	states := []string{"waiting", "preparing", "downloading", "paused", "merging", "finished", "failed", "cancelled"}
	if task.Status < 0 || task.Status >= len(states) {
		return nil, workflowFailure(id, "status query", errors.New("unknown task status"))
	}
	task.State = states[task.Status]
	task.Name = printableTitle(task.Name)
	for i := range task.Files {
		task.Files[i].Name = printableTitle(task.Files[i].Name)
	}
	return &task, nil
}

func printableTitle(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	if len(value) > 400 {
		value = string([]rune(value)[:min(len([]rune(value)), 100)])
	}
	return value
}

func (s *Server) ChannelsAvailable(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := s.get_wxchannels_status(ctx)
	if err != nil {
		return false, errors.New("local service unavailable; start wxc and open a Channels page in WeChat")
	}
	var status struct {
		Available bool `json:"available"`
	}
	if err := decodeStructured(result, &status); err != nil {
		return false, errors.New("invalid Channels connection status")
	}
	return status.Available, nil
}

// PersonalToolNames is the narrow default catalog for the wxc stdio entry point.
func PersonalToolNames() []string {
	return []string{"download_video", "get_video_task", "verify_video", "get_wxchannels_status"}
}

func local_video_tool_definitions() []any {
	return []any{
		wxchannels_tool_definition("download_video", "Download and verify a Channels video", "Resolve through the active local WeChat session, create one native task, wait, and verify the actual local output. Does not enable capture or change system settings.", map[string]any{
			"type": "object", "properties": map[string]any{
				"url":          map[string]any{"type": "string", "description": "HTTPS WeChat Channels share URL"},
				"download_dir": map[string]any{"type": "string", "description": "Destination directory; defaults to ~/Downloads"},
				"filename":     map[string]any{"type": "string"}, "spec": map[string]any{"type": "string"},
				"existing_action": map[string]any{"type": "string", "enum": []string{"error", "skip", "overwrite", "duplicate"}, "default": "error"},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 21600, "default": 900},
				"decode":          map[string]any{"type": "boolean", "default": false},
			}, "required": []string{"url"}, "additionalProperties": false,
		}),
		wxchannels_tool_definition("get_video_task", "Inspect a download task", "Read a safe projection of one native task, omitting signed URLs, keys, and raw backend errors.", map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"id"}, "additionalProperties": false}),
		wxchannels_tool_definition("verify_video", "Verify a local video", "Inspect actual dimensions/duration and SHA-256; optionally decode every frame. Requires local FFmpeg tools and does not access the network.", map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "decode": map[string]any{"type": "boolean", "default": false}}, "required": []string{"path"}, "additionalProperties": false}),
	}
}

func (s *Server) download_video_tool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var request VideoDownloadRequest
	if err := decode_tool_arguments(raw, &request); err != nil {
		return nil, err
	}
	result, err := s.DownloadVideo(ctx, request)
	if err != nil {
		return nil, err
	}
	return successful_tool_result(result)
}

func (s *Server) video_task_tool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var request struct {
		ID int `json:"id"`
	}
	if err := decode_tool_arguments(raw, &request); err != nil {
		return nil, err
	}
	task, err := s.VideoTask(ctx, request.ID)
	if err != nil {
		return nil, err
	}
	return successful_tool_result(task)
}

func (s *Server) verify_video_tool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var request struct {
		Path   string `json:"path"`
		Decode bool   `json:"decode"`
	}
	if err := decode_tool_arguments(raw, &request); err != nil {
		return nil, err
	}
	if request.Path == "" {
		return nil, errors.New("path is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	report, err := s.verifier().Verify(ctx, request.Path, request.Decode)
	if err != nil {
		return nil, err
	}
	return successful_tool_result(report)
}
