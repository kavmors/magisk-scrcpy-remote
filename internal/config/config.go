package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Listen                 string      `json:"listen"`
	StateDir               string      `json:"stateDir"`
	ScrcpyServerPath       string      `json:"scrcpyServerPath"`
	DeviceScrcpyServerPath string      `json:"deviceScrcpyServerPath"`
	Video                  VideoConfig `json:"video"`
	Audio                  AudioConfig `json:"audio"`
}

type VideoConfig struct {
	MaxSize int `json:"maxSize"`
	MaxFps  int `json:"maxFps"`
	BitRate int `json:"bitRate"`
}

type AudioConfig struct {
	Enabled bool   `json:"enabled"`
	BitRate int    `json:"bitRate"`
	Source  string `json:"source"`
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return Config{}, err
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return Config{}, err
		}
	}
	normalize(&cfg)
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Defaults() Config {
	return Config{
		Listen:                 "0.0.0.0:13014",
		StateDir:               "/data/adb/magisk-scrcpy-remote",
		ScrcpyServerPath:       "/data/adb/modules/magisk-scrcpy-remote/bin/scrcpy-server-v4.0",
		DeviceScrcpyServerPath: "/data/local/tmp/msr-scrcpy-server.jar",
		Video: VideoConfig{
			MaxSize: 1280,
			MaxFps:  30,
			BitRate: 4000000,
		},
		Audio: AudioConfig{
			Enabled: true,
			BitRate: 128000,
			Source:  "output",
		},
	}
}

func EnsureState(cfg Config) error {
	if err := os.MkdirAll(filepath.Join(cfg.StateDir, "logs"), 0o700); err != nil {
		return err
	}
	return nil
}

func ModuleTokenPath(moduleDir string) (string, error) {
	if moduleDir == "" {
		return "", errors.New("module directory is required for token loading")
	}
	return filepath.Join(moduleDir, "token"), nil
}

func ModuleTokenLocalPath(moduleDir string) (string, error) {
	if moduleDir == "" {
		return "", errors.New("module directory is required for token loading")
	}
	return filepath.Join(moduleDir, "token.local"), nil
}

func WaitModuleToken(ctx context.Context, moduleDir string, interval time.Duration) (string, error) {
	tokenPath, err := ModuleTokenPath(moduleDir)
	if err != nil {
		return "", err
	}
	tokenLocalPath, err := ModuleTokenLocalPath(moduleDir)
	if err != nil {
		return "", err
	}
	for {
		token, err := readFirstToken(tokenPath, tokenLocalPath)
		if err != nil {
			return "", err
		}
		if token != "" {
			return token, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
	}
}

func readFirstToken(paths ...string) (string, error) {
	for _, path := range paths {
		token, err := readToken(path)
		if err != nil {
			return "", err
		}
		if token != "" {
			return token, nil
		}
	}
	return "", nil
}

func readToken(tokenPath string) (string, error) {
	if b, err := os.ReadFile(tokenPath); err == nil {
		return stringTrimSpace(string(b)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return "", nil
}

func normalize(cfg *Config) {
	if cfg.Listen == "" {
		cfg.Listen = "0.0.0.0:13014"
	}
	if cfg.StateDir == "" {
		cfg.StateDir = "/data/adb/magisk-scrcpy-remote"
	}
	if cfg.DeviceScrcpyServerPath == "" {
		cfg.DeviceScrcpyServerPath = "/data/local/tmp/msr-scrcpy-server.jar"
	}
	if cfg.Video.MaxSize == 0 {
		cfg.Video.MaxSize = 1280
	}
	if cfg.Video.MaxFps == 0 {
		cfg.Video.MaxFps = 30
	}
	if cfg.Video.BitRate == 0 {
		cfg.Video.BitRate = 4000000
	}
	if cfg.Audio.BitRate == 0 {
		cfg.Audio.BitRate = 128000
	}
	if cfg.Audio.Source == "" {
		cfg.Audio.Source = "output"
	}
}

func validate(cfg Config) error {
	if cfg.ScrcpyServerPath == "" {
		return errors.New("scrcpyServerPath is required")
	}
	if cfg.Audio.Source != "output" && cfg.Audio.Source != "playback" && cfg.Audio.Source != "mic" {
		return fmt.Errorf("unsupported audio source %q", cfg.Audio.Source)
	}
	return nil
}

func stringTrimSpace(s string) string {
	for len(s) > 0 {
		switch s[0] {
		case ' ', '\n', '\r', '\t':
			s = s[1:]
		default:
			goto tail
		}
	}
tail:
	for len(s) > 0 {
		switch s[len(s)-1] {
		case ' ', '\n', '\r', '\t':
			s = s[:len(s)-1]
		default:
			return s
		}
	}
	return s
}
