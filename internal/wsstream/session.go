package wsstream

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/kavmors/magisk-scrcpy-remote/internal/config"
	"github.com/kavmors/magisk-scrcpy-remote/internal/controlbridge"
	"github.com/kavmors/magisk-scrcpy-remote/internal/scrcpy"
	"nhooyr.io/websocket"
)

const (
	streamVideo byte = 1
	streamAudio byte = 2

	flagConfig   byte = 1 << 0
	flagKeyFrame byte = 1 << 1
)

type wsWriter struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func Run(ctx context.Context, ws *websocket.Conn, cfg config.Config) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sc, err := scrcpy.Start(sctx, cfg)
	if err != nil {
		return err
	}
	defer sc.Close()

	writer := &wsWriter{ws: ws}
	bridge := controlbridge.New(scrcpy.NewController(sc.Control))

	if err := writer.writeJSON(ctx, map[string]any{
		"type":  "hello",
		"mode":  "websocket",
		"video": "h264",
		"audio": map[string]any{"enabled": cfg.Audio.Enabled, "codec": "opus"},
	}); err != nil {
		return err
	}

	errCh := make(chan error, 3)
	go func() { errCh <- pumpVideo(sctx, sc.Video, writer, bridge) }()
	if cfg.Audio.Enabled && sc.Audio != nil {
		go func() { errCh <- pumpAudio(sctx, sc.Audio, writer) }()
	}
	go func() { errCh <- readControl(sctx, ws, bridge) }()

	select {
	case <-sctx.Done():
		return nil
	case err := <-errCh:
		if err == nil || err == io.EOF || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			return nil
		}
		cancel()
		return err
	}
}

func readControl(ctx context.Context, ws *websocket.Conn, bridge *controlbridge.Bridge) error {
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return err
		}
		if typ == websocket.MessageText {
			bridge.Handle(data)
		}
	}
}

func pumpVideo(ctx context.Context, r io.Reader, writer *wsWriter, bridge *controlbridge.Bridge) error {
	sr := scrcpy.NewStreamReader("video", r, true)
	codec, err := sr.ReadCodecID()
	if err != nil {
		return err
	}
	if codec != scrcpy.CodecH264 {
		return fmt.Errorf("unexpected video codec 0x%08x", codec)
	}
	log.Printf("ws stream video codec: h264")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		pkt, sess, err := sr.Next()
		if err != nil {
			return err
		}
		if sess != nil {
			bridge.SetSize(sess.Width, sess.Height)
			log.Printf("ws stream video session: %dx%d", sess.Width, sess.Height)
			if err := writer.writeJSON(ctx, map[string]any{
				"type":   "video-session",
				"width":  sess.Width,
				"height": sess.Height,
			}); err != nil {
				return err
			}
			continue
		}
		if pkt == nil {
			continue
		}
		flags := byte(0)
		if pkt.Config {
			flags |= flagConfig
		}
		if pkt.KeyFrame {
			flags |= flagKeyFrame
		}
		if err := writer.writeMedia(ctx, streamVideo, flags, pkt.PTS, pkt.Data); err != nil {
			return err
		}
	}
}

func pumpAudio(ctx context.Context, r io.Reader, writer *wsWriter) error {
	sr := scrcpy.NewStreamReader("audio", r, false)
	codec, err := sr.ReadCodecID()
	if err != nil {
		return err
	}
	if codec == 0 {
		log.Printf("ws stream audio disabled by device")
		_ = writer.writeJSON(ctx, map[string]any{"type": "audio-disabled"})
		return nil
	}
	if codec != scrcpy.CodecOpus {
		return fmt.Errorf("unexpected audio codec 0x%08x", codec)
	}
	log.Printf("ws stream audio codec: opus")
	if err := writer.writeJSON(ctx, map[string]any{
		"type":       "audio-codec",
		"codec":      "opus",
		"sampleRate": 48000,
		"channels":   2,
	}); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		pkt, _, err := sr.Next()
		if err != nil {
			return err
		}
		if pkt == nil || pkt.Config {
			continue
		}
		if err := writer.writeMedia(ctx, streamAudio, 0, pkt.PTS, pkt.Data); err != nil {
			return err
		}
	}
}

func (w *wsWriter) writeJSON(ctx context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return w.ws.Write(writeCtx, websocket.MessageText, data)
}

func (w *wsWriter) writeMedia(ctx context.Context, stream, flags byte, pts uint64, payload []byte) error {
	frame := make([]byte, 10+len(payload))
	frame[0] = stream
	frame[1] = flags
	binary.BigEndian.PutUint64(frame[2:10], pts)
	copy(frame[10:], payload)

	w.mu.Lock()
	defer w.mu.Unlock()
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return w.ws.Write(writeCtx, websocket.MessageBinary, frame)
}
