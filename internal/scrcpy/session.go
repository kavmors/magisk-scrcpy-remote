package scrcpy

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/kavmors/magisk-scrcpy-remote/internal/config"
)

type Session struct {
	Video   net.Conn
	Audio   net.Conn
	Control net.Conn
	Cmd     *exec.Cmd
	SCID    uint32
}

func Start(ctx context.Context, cfg config.Config) (*Session, error) {
	if err := installServer(cfg.ScrcpyServerPath, cfg.DeviceScrcpyServerPath); err != nil {
		return nil, err
	}

	scid := rand.Uint32() & 0x7fffffff
	args := serverArgs(scid, cfg)
	cmd := exec.CommandContext(ctx, "/system/bin/app_process", args...)
	cmd.Env = append(os.Environ(), "CLASSPATH="+cfg.DeviceScrcpyServerPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	socketName := fmt.Sprintf("@scrcpy_%08x", scid)
	video, audio, control, err := connectAll(ctx, socketName, cfg.Audio.Enabled)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}

	log.Printf("scrcpy server started pid=%d scid=%08x", cmd.Process.Pid, scid)
	return &Session{
		Video:   video,
		Audio:   audio,
		Control: control,
		Cmd:     cmd,
		SCID:    scid,
	}, nil
}

func (s *Session) Close() {
	if s.Video != nil {
		_ = s.Video.Close()
	}
	if s.Audio != nil {
		_ = s.Audio.Close()
	}
	if s.Control != nil {
		_ = s.Control.Close()
	}
	if s.Cmd != nil && s.Cmd.Process != nil {
		_ = s.Cmd.Process.Kill()
		_ = s.Cmd.Wait()
	}
}

func installServer(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open scrcpy server: %w", err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return os.Chmod(dst, 0o644)
}

func serverArgs(scid uint32, cfg config.Config) []string {
	args := []string{
		"/",
		"com.genymobile.scrcpy.Server",
		"4.0",
		fmt.Sprintf("scid=%08x", scid),
		"log_level=info",
		"tunnel_forward=true",
		"send_dummy_byte=false",
		"send_device_meta=false",
		"cleanup=false",
		"power_on=true",
		"control=true",
		"video=true",
		"video_codec=h264",
		"audio_codec=opus",
		fmt.Sprintf("max_size=%d", cfg.Video.MaxSize),
		fmt.Sprintf("max_fps=%d", cfg.Video.MaxFps),
		fmt.Sprintf("video_bit_rate=%d", cfg.Video.BitRate),
	}
	if cfg.Audio.Enabled {
		args = append(args,
			"audio=true",
			fmt.Sprintf("audio_bit_rate=%d", cfg.Audio.BitRate),
		)
		if cfg.Audio.Source != "output" {
			args = append(args, "audio_source="+cfg.Audio.Source)
		}
	} else {
		args = append(args, "audio=false")
	}
	return args
}

func connectAll(ctx context.Context, socketName string, withAudio bool) (net.Conn, net.Conn, net.Conn, error) {
	video, err := dialAbstract(ctx, socketName)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect video socket: %w", err)
	}
	var audio net.Conn
	if withAudio {
		audio, err = dialAbstract(ctx, socketName)
		if err != nil {
			_ = video.Close()
			return nil, nil, nil, fmt.Errorf("connect audio socket: %w", err)
		}
	}
	control, err := dialAbstract(ctx, socketName)
	if err != nil {
		_ = video.Close()
		if audio != nil {
			_ = audio.Close()
		}
		return nil, nil, nil, fmt.Errorf("connect control socket: %w", err)
	}
	return video, audio, control, nil
}

func dialAbstract(ctx context.Context, name string) (net.Conn, error) {
	var last error
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		conn, err := net.DialTimeout("unix", name, 500*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	return nil, last
}
