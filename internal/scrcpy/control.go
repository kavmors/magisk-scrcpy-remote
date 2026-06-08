package scrcpy

import (
	"encoding/binary"
	"io"
	"math"
)

const (
	controlInjectKeycode    = 0
	controlInjectText       = 1
	controlInjectTouchEvent = 2
	controlInjectScroll     = 3
	controlBackOrScreenOn   = 4
	controlSetDisplayPower  = 10

	keyActionDown = 0
	keyActionUp   = 1

	motionActionDown = 0
	motionActionUp   = 1
	motionActionMove = 2

	pointerIDMouse = ^uint64(0)

	buttonPrimary = 1
)

type Controller struct {
	w io.Writer
}

func NewController(w io.Writer) *Controller {
	return &Controller{w: w}
}

func (c *Controller) Touch(action byte, x, y int32, screenW, screenH uint16, buttons uint32) error {
	var b [32]byte
	b[0] = controlInjectTouchEvent
	b[1] = action
	binary.BigEndian.PutUint64(b[2:10], pointerIDMouse)
	binary.BigEndian.PutUint32(b[10:14], uint32(x))
	binary.BigEndian.PutUint32(b[14:18], uint32(y))
	binary.BigEndian.PutUint16(b[18:20], screenW)
	binary.BigEndian.PutUint16(b[20:22], screenH)
	pressure := uint16(0)
	if action != motionActionUp {
		pressure = 0xffff
	}
	binary.BigEndian.PutUint16(b[22:24], pressure)
	actionButton := uint32(0)
	if action == motionActionDown {
		actionButton = buttonPrimary
	}
	binary.BigEndian.PutUint32(b[24:28], actionButton)
	binary.BigEndian.PutUint32(b[28:32], buttons)
	_, err := c.w.Write(b[:])
	return err
}

func (c *Controller) Scroll(x, y int32, screenW, screenH uint16, h, v float64) error {
	var b [21]byte
	b[0] = controlInjectScroll
	binary.BigEndian.PutUint32(b[1:5], uint32(x))
	binary.BigEndian.PutUint32(b[5:9], uint32(y))
	binary.BigEndian.PutUint16(b[9:11], screenW)
	binary.BigEndian.PutUint16(b[11:13], screenH)
	binary.BigEndian.PutUint16(b[13:15], uint16(floatToI16FP(clamp(h/16, -1, 1))))
	binary.BigEndian.PutUint16(b[15:17], uint16(floatToI16FP(clamp(v/16, -1, 1))))
	binary.BigEndian.PutUint32(b[17:21], 0)
	_, err := c.w.Write(b[:])
	return err
}

func (c *Controller) Keycode(action byte, keycode uint32, meta uint32) error {
	var b [14]byte
	b[0] = controlInjectKeycode
	b[1] = action
	binary.BigEndian.PutUint32(b[2:6], keycode)
	binary.BigEndian.PutUint32(b[6:10], 0)
	binary.BigEndian.PutUint32(b[10:14], meta)
	_, err := c.w.Write(b[:])
	return err
}

func (c *Controller) Back(action byte) error {
	_, err := c.w.Write([]byte{controlBackOrScreenOn, action})
	return err
}

func (c *Controller) Text(text string) error {
	if len(text) > 300 {
		text = text[:300]
	}
	b := make([]byte, 5+len(text))
	b[0] = controlInjectText
	binary.BigEndian.PutUint32(b[1:5], uint32(len(text)))
	copy(b[5:], text)
	_, err := c.w.Write(b)
	return err
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func floatToI16FP(v float64) int16 {
	if v >= 1 {
		return math.MaxInt16
	}
	if v <= -1 {
		return math.MinInt16
	}
	return int16(math.Round(v * 32768))
}

func MotionAction(name string) byte {
	switch name {
	case "down":
		return motionActionDown
	case "up":
		return motionActionUp
	default:
		return motionActionMove
	}
}

func KeyAction(name string) byte {
	if name == "up" {
		return keyActionUp
	}
	return keyActionDown
}

func ActiveButtons(down bool) uint32 {
	if down {
		return buttonPrimary
	}
	return 0
}
