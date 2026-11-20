package mediaverify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validProbe = `{"streams":[{"codec_type":"video","codec_name":"h264","width":1280,"height":720,"duration":"2.0","r_frame_rate":"30000/1001"},{"codec_type":"audio","codec_name":"aac"}],"format":{"duration":"2.25"}}`

func TestVerifyUsesMeasuredFileAndOptionalDecode(t *testing.T) {
	for _, decode := range []bool{false, true} {
		t.Run(map[bool]string{false: "probe", true: "decode"}[decode], func(t *testing.T) {
			content := []byte("generated test data, not a personal video")
			path := filepath.Join(t.TempDir(), "video.mp4")
			if err := os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			verifier := &Verifier{run: func(ctx context.Context, tool string, args ...string) ([]byte, error) {
				calls++
				joined := strings.Join(args, " ")
				if !strings.Contains(joined, "-protocol_whitelist file,pipe") {
					t.Fatalf("network protocols not restricted: %v", args)
				}
				if tool == "ffprobe" {
					return []byte(validProbe), nil
				}
				if !decode || tool != "ffmpeg" || !strings.Contains(joined, "-xerror") {
					t.Fatalf("unexpected decode command: %s %v", tool, args)
				}
				return nil, nil
			}}
			report, err := verifier.Verify(context.Background(), path, decode)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(content)
			if report.SHA256 != hex.EncodeToString(hash[:]) || report.Width != 1280 || report.Height != 720 || report.Duration != 2.25 || report.Size != int64(len(content)) || report.AudioCodec != "aac" {
				t.Fatalf("wrong report: %+v", report)
			}
			if calls != 1+map[bool]int{false: 0, true: 1}[decode] || report.Validation != map[bool]string{false: "probe", true: "decode"}[decode] {
				t.Fatalf("wrong validation level/calls: %+v / %d", report, calls)
			}
		})
	}
}

func TestProbeRejectsInvalidOrAudioOnlyMedia(t *testing.T) {
	for _, data := range []string{
		`not json`, `{}`, `{"streams":[{"codec_type":"audio","codec_name":"aac"}],"format":{"duration":"2"}}`,
		`{"streams":[{"codec_type":"video","codec_name":"h264","width":1280,"height":720}],"format":{"duration":"NaN"}}`,
		`{"streams":[{"codec_type":"video","codec_name":"h264","width":1280,"height":720}],"format":{"duration":"+Inf"}}`,
	} {
		if _, err := parseProbe([]byte(data)); err == nil {
			t.Errorf("accepted invalid output: %s", data)
		}
	}
}

func TestVerifyRejectsMissingEmptyAndNonRegularFiles(t *testing.T) {
	directory := t.TempDir()
	empty := filepath.Join(directory, "empty.mp4")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, empty, filepath.Join(directory, "missing.mp4")} {
		if _, err := New().Verify(context.Background(), path, false); err == nil {
			t.Errorf("accepted %s", path)
		}
	}
}

func TestDecodeFailureDoesNotLeakToolStderr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	verifier := &Verifier{run: func(ctx context.Context, tool string, args ...string) ([]byte, error) {
		if tool == "ffprobe" {
			return []byte(validProbe), nil
		}
		return nil, errors.New("https://example.invalid/media?secret=DO_NOT_PRINT")
	}}
	_, err := verifier.Verify(context.Background(), path, true)
	if err == nil || strings.Contains(err.Error(), "DO_NOT_PRINT") || strings.Contains(err.Error(), "https://") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestVerifyRejectsFileChangedDuringInspection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	verifier := &Verifier{run: func(ctx context.Context, tool string, args ...string) ([]byte, error) {
		if err := os.WriteFile(path, []byte("changed length"), 0600); err != nil {
			t.Fatal(err)
		}
		return []byte(validProbe), nil
	}}
	if _, err := verifier.Verify(context.Background(), path, false); err == nil {
		t.Fatal("accepted a file that changed")
	}
}

func TestCancelledHashReturnsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	verifier := &Verifier{run: func(ctx context.Context, tool string, args ...string) ([]byte, error) {
		cancel()
		return []byte(validProbe), nil
	}}
	if _, err := verifier.Verify(ctx, path, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
