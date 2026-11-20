// Package mediaverify inspects actual local output, never remote metadata.
package mediaverify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Report describes the bytes on disk. Decoded means a full ffmpeg decode passed.
type Report struct {
	Path       string  `json:"path"`
	Size       int64   `json:"size"`
	SHA256     string  `json:"sha256"`
	VideoCodec string  `json:"video_codec"`
	AudioCodec string  `json:"audio_codec,omitempty"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Duration   float64 `json:"duration_seconds"`
	FPS        float64 `json:"fps,omitempty"`
	Validation string  `json:"validation"`
}

type commandRunner func(context.Context, string, ...string) ([]byte, error)

// Verifier uses separately installed ffprobe, and optionally ffmpeg. It does not
// fetch tools, convert media, or allow network protocols while inspecting files.
type Verifier struct {
	FFprobe string
	FFmpeg  string
	run     commandRunner
}

func New() *Verifier { return &Verifier{} }

func (v *Verifier) CheckTools(decode bool) error {
	if _, err := v.tool(v.FFprobe, "ffprobe"); err != nil {
		return err
	}
	if decode {
		_, err := v.tool(v.FFmpeg, "ffmpeg")
		return err
	}
	return nil
}

func (v *Verifier) tool(configured, fallback string) (string, error) {
	if configured == "" {
		configured = fallback
	}
	if v.run != nil {
		return configured, nil
	}
	path, err := exec.LookPath(configured)
	if err != nil {
		return "", fmt.Errorf("%s is required for media verification; install FFmpeg first", fallback)
	}
	return filepath.Abs(path)
}

func (v *Verifier) Verify(ctx context.Context, path string, decode bool) (Report, error) {
	if v == nil {
		v = New()
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return Report{}, errors.New("invalid local media path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return Report{}, errors.New("the output is missing, empty, or not a regular file")
	}
	probe, err := v.tool(v.FFprobe, "ffprobe")
	if err != nil {
		return Report{}, err
	}
	run := v.run
	if run == nil {
		run = runBounded
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	data, err := run(probeCtx, probe,
		"-v", "error", "-protocol_whitelist", "file,pipe",
		"-show_entries", "format=duration:stream=codec_type,codec_name,width,height,duration,r_frame_rate",
		"-of", "json", path)
	cancel()
	if err != nil {
		return Report{}, fmt.Errorf("ffprobe could not validate the output: %w", commandError(ctx, err))
	}
	report, err := parseProbe(data)
	if err != nil {
		return Report{}, err
	}
	report.Path, report.Size, report.Validation = path, info.Size(), "probe"
	if decode {
		ffmpeg, err := v.tool(v.FFmpeg, "ffmpeg")
		if err != nil {
			return Report{}, err
		}
		if _, err := run(ctx, ffmpeg,
			"-nostdin", "-v", "error", "-xerror", "-protocol_whitelist", "file,pipe",
			"-i", path, "-map", "0:v:0", "-map", "0:a?", "-f", "null", "-"); err != nil {
			return Report{}, fmt.Errorf("full media decode failed: %w", commandError(ctx, err))
		}
		report.Validation = "decode"
	}
	file, err := os.Open(path)
	if err != nil {
		return Report{}, errors.New("could not open the verified output for hashing")
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 128*1024)
	var read int64
	for {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
			read += int64(n)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return Report{}, errors.New("could not read the verified output completely")
		}
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(info, after) || read != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return Report{}, errors.New("the output changed during verification; verify it again after writing stops")
	}
	report.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return report, nil
}

func commandError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Do not surface stderr: it can contain embedded URLs or untrusted metadata.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	return errors.New("tool failed, exceeded its time/output limit, or media is invalid")
}

func parseProbe(data []byte) (Report, error) {
	var probe struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type     string `json:"codec_type"`
			Codec    string `json:"codec_name"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Duration string `json:"duration"`
			Rate     string `json:"r_frame_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Report{}, errors.New("ffprobe returned invalid JSON")
	}
	report := Report{}
	for _, stream := range probe.Streams {
		if stream.Type == "video" && report.VideoCodec == "" {
			report.VideoCodec, report.Width, report.Height = stream.Codec, stream.Width, stream.Height
			report.Duration = positiveFloat(stream.Duration)
			parts := strings.Split(stream.Rate, "/")
			if len(parts) == 2 {
				if denominator := positiveFloat(parts[1]); denominator > 0 {
					fps := positiveFloat(parts[0]) / denominator
					if !math.IsInf(fps, 0) && !math.IsNaN(fps) {
						report.FPS = fps
					}
				}
			}
		}
		if stream.Type == "audio" && report.AudioCodec == "" {
			report.AudioCodec = stream.Codec
		}
	}
	if duration := positiveFloat(probe.Format.Duration); duration > 0 {
		report.Duration = duration
	}
	if report.VideoCodec == "" || report.Width <= 0 || report.Height <= 0 || report.Duration <= 0 {
		return Report{}, errors.New("the output has no valid video stream, dimensions, or duration")
	}
	return report, nil
}

func positiveFloat(value string) float64 {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number <= 0 || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0
	}
	return number
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 1024*1024 {
		return 0, errors.New("tool output exceeded 1 MiB")
	}
	return b.Buffer.Write(data)
}

func runBounded(ctx context.Context, executable string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	var output limitedBuffer
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
