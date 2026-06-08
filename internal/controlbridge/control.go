package controlbridge

import (
	"encoding/json"
	"log"
	"math"
	"sync"

	"github.com/kavmors/magisk-scrcpy-remote/internal/scrcpy"
)

type Bridge struct {
	mu     sync.Mutex
	ctrl   *scrcpy.Controller
	width  uint16
	height uint16
}

type event struct {
	Type           string  `json:"type"`
	Action         string  `json:"action"`
	X              float64 `json:"x"`
	Y              float64 `json:"y"`
	DeltaX         float64 `json:"deltaX"`
	DeltaY         float64 `json:"deltaY"`
	AndroidKeyCode uint32  `json:"androidKeyCode"`
	Text           string  `json:"text"`
}

func New(ctrl *scrcpy.Controller) *Bridge {
	return &Bridge{ctrl: ctrl, width: 1, height: 1}
}

func (b *Bridge) SetSize(w, h uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if w > 0 && w <= math.MaxUint16 {
		b.width = uint16(w)
	}
	if h > 0 && h <= math.MaxUint16 {
		b.height = uint16(h)
	}
}

func (b *Bridge) Handle(data []byte) {
	var ev event
	if err := json.Unmarshal(data, &ev); err != nil {
		log.Printf("control: invalid json: %v", err)
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	switch ev.Type {
	case "touch":
		x, y := b.point(ev.X, ev.Y)
		down := ev.Action != "up"
		if err := b.ctrl.Touch(scrcpy.MotionAction(ev.Action), x, y, b.width, b.height, scrcpy.ActiveButtons(down)); err != nil {
			log.Printf("control: touch failed: %v", err)
		}
	case "scroll":
		x, y := b.point(ev.X, ev.Y)
		if err := b.ctrl.Scroll(x, y, b.width, b.height, -ev.DeltaX/100, -ev.DeltaY/100); err != nil {
			log.Printf("control: scroll failed: %v", err)
		}
	case "keycode":
		if ev.AndroidKeyCode == 0 {
			return
		}
		if err := b.ctrl.Keycode(scrcpy.KeyAction(ev.Action), ev.AndroidKeyCode, 0); err != nil {
			log.Printf("control: keycode failed: %v", err)
		}
	case "back":
		if err := b.ctrl.Back(0); err != nil {
			log.Printf("control: back down failed: %v", err)
			return
		}
		if err := b.ctrl.Back(1); err != nil {
			log.Printf("control: back up failed: %v", err)
		}
	case "text":
		if ev.Text != "" {
			if err := b.ctrl.Text(ev.Text); err != nil {
				log.Printf("control: text failed: %v", err)
			}
		}
	}
}

func (b *Bridge) point(nx, ny float64) (int32, int32) {
	if nx < 0 {
		nx = 0
	}
	if nx > 1 {
		nx = 1
	}
	if ny < 0 {
		ny = 0
	}
	if ny > 1 {
		ny = 1
	}
	return int32(math.Round(nx * float64(b.width))), int32(math.Round(ny * float64(b.height)))
}
