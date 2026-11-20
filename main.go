package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"wx_channel/cmd"
	"wx_channel/internal/config"
	"wx_channel/internal/logtime"
)

var AppVer = "0.1.0+src261106"
var Mode = "release"

func main() {
	if Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	home, err := runtimeHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot prepare private runtime directory:", err)
		os.Exit(1)
	}
	if strings.TrimSpace(os.Getenv(config.EnvConfigPath)) == "" {
		if err := os.Setenv(config.EnvConfigPath, filepath.Join(home, "config.yaml")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	logger, logFile, logPath, err := newAppLogger(home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot initialize private log:", err)
		os.Exit(1)
	}
	cfg := config.New(AppVer, Mode, logger, logFile, logPath)
	err = cmd.Execute(cfg)
	_ = logFile.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func runtimeHome() (string, error) {
	home := strings.TrimSpace(os.Getenv("WXC_HOME"))
	if home == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(base, "wxchannels-kit")
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return "", err
	}
	return absolute, nil
}

func newAppLogger(home string) (*zerolog.Logger, *os.File, string, error) {
	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	zerolog.TimeFieldFormat = time.RFC3339Nano
	directory := filepath.Join(home, "logs")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, nil, "", err
	}
	// CreateTemp is exclusive and 0600; never truncate another process's log.
	file, err := os.CreateTemp(directory, "wxc-*.log")
	if err != nil {
		return nil, nil, "", err
	}
	logger := zerolog.New(file).Hook(logtime.Hook{})
	return &logger, file, file.Name(), nil
}
