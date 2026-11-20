package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"wx_channel/internal/mcpserver"
	"wx_channel/internal/mediaverify"
)

func addLocalAPIFlag(command *cobra.Command, value *string) {
	command.Flags().StringVar(value, "api-base-url", "", "Local wxc API URL (defaults to the active configuration)")
}

func localControl(apiURL string) (*mcpserver.Server, error) {
	if apiURL == "" {
		apiURL = configured_api_base_url()
	}
	return mcpserver.NewServer(mcpserver.Config{APIBaseURL: apiURL, Version: Version, AllowedTools: mcpserver.PersonalToolNames()})
}

func printJSON(command *cobra.Command, value any) error {
	encoder := json.NewEncoder(command.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func newDownloadCommand() *cobra.Command {
	var apiURL, output, name, spec, existing string
	var timeout time.Duration
	var decode bool
	command := &cobra.Command{
		Use: "download <share-url>", Short: "Download through the local WeChat session and verify the actual video",
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if timeout < time.Second || timeout > 6*time.Hour {
				return errors.New("--timeout must be between 1s and 6h")
			}
			client, err := localControl(apiURL)
			if err != nil {
				return err
			}
			result, err := client.DownloadVideo(command.Context(), mcpserver.VideoDownloadRequest{
				URL: args[0], DownloadDir: output, Filename: name, Spec: spec,
				ExistingAction: existing, TimeoutSeconds: int(timeout / time.Second), Decode: decode,
			})
			if err != nil {
				if result != nil {
					fmt.Fprintf(command.ErrOrStderr(), "Task %d was submitted; do not assume the task stopped when this command exits.\n", result.TaskID)
				}
				return err
			}
			return printJSON(command, result)
		},
	}
	addLocalAPIFlag(command, &apiURL)
	command.Flags().StringVarP(&output, "output", "o", "~/Downloads", "Destination directory (passed to the native task before download)")
	command.Flags().StringVar(&name, "filename", "", "Optional native filename override, without directory components")
	command.Flags().StringVar(&spec, "spec", "", "Advertised video variant identifier; omitted uses the native default")
	command.Flags().StringVar(&existing, "existing", "error", "Existing task policy: error, skip, overwrite, duplicate")
	command.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "Overall resolve/download/verification wait timeout; does not cancel an already submitted task")
	command.Flags().BoolVar(&decode, "decode", true, "Decode every frame with ffmpeg after probing the output")
	return command
}

func newTaskCommand() *cobra.Command {
	var apiURL string
	command := &cobra.Command{Use: "task <id>", Short: "Inspect one native task without printing URLs, keys, or raw errors", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		id, err := strconv.Atoi(args[0])
		if err != nil || id <= 0 {
			return errors.New("task ID must be a positive integer")
		}
		client, err := localControl(apiURL)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(command.Context(), 15*time.Second)
		defer cancel()
		task, err := client.VideoTask(ctx, id)
		if err != nil {
			return err
		}
		return printJSON(command, task)
	}}
	addLocalAPIFlag(command, &apiURL)
	return command
}

func newVerifyCommand() *cobra.Command {
	var decode bool
	var timeout time.Duration
	command := &cobra.Command{Use: "verify <local-video>", Short: "Measure a local video and calculate its SHA-256 without contacting a service", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		if timeout < time.Second || timeout > 6*time.Hour {
			return errors.New("--timeout must be between 1s and 6h")
		}
		ctx, cancel := context.WithTimeout(command.Context(), timeout)
		defer cancel()
		report, err := mediaverify.New().Verify(ctx, args[0], decode)
		if err != nil {
			return err
		}
		return printJSON(command, report)
	}}
	command.Flags().BoolVar(&decode, "decode", false, "Decode every video/audio frame, not just container/stream probing")
	command.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "Maximum local verification duration")
	return command
}

func newDoctorCommand() *cobra.Command {
	var apiURL string
	command := &cobra.Command{Use: "doctor", Short: "Check local tools and the active WeChat connection without enabling capture", Args: cobra.NoArgs, RunE: func(command *cobra.Command, args []string) error {
		if apiURL == "" {
			apiURL = configured_api_base_url()
		}
		canonical, err := mcpserver.LocalAPIURL(apiURL)
		if err != nil {
			return err
		}
		_, probeErr := exec.LookPath("ffprobe")
		_, decodeErr := exec.LookPath("ffmpeg")
		client, err := localControl(canonical)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(command.Context(), 10*time.Second)
		defer cancel()
		available, connectionErr := client.ChannelsAvailable(ctx)
		report := struct {
			Version          string `json:"version"`
			API              string `json:"api"`
			FFprobe          bool   `json:"ffprobe"`
			FFmpeg           bool   `json:"ffmpeg"`
			ServiceReachable bool   `json:"service_reachable"`
			WeChatConnected  bool   `json:"wechat_connected"`
			ChangesMade      bool   `json:"system_changes_made"`
		}{Version, canonical, probeErr == nil, decodeErr == nil, connectionErr == nil, available, false}
		if err := printJSON(command, report); err != nil {
			return err
		}
		if probeErr != nil || decodeErr != nil {
			return errors.New("install FFmpeg so ffprobe and ffmpeg are on PATH")
		}
		if connectionErr != nil {
			return connectionErr
		}
		if !available {
			return errors.New("local service is running, but no Channels page is connected; open a video in desktop WeChat")
		}
		return nil
	}}
	addLocalAPIFlag(command, &apiURL)
	return command
}

func init() {
	root_cmd.AddCommand(newDownloadCommand(), newTaskCommand(), newVerifyCommand(), newDoctorCommand())
}
