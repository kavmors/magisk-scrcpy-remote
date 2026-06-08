package scrcpy

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	CodecH264 = 0x68323634
	CodecOpus = 0x6f707573

	packetFlagSession  = uint64(1) << 63
	packetFlagConfig   = uint64(1) << 62
	packetFlagKeyFrame = uint64(1) << 61
	packetPTSMask      = packetFlagKeyFrame - 1
)

type MediaPacket struct {
	Data     []byte
	PTS      uint64
	Config   bool
	KeyFrame bool
}

type VideoSession struct {
	Width  uint32
	Height uint32
}

type StreamReader struct {
	r       io.Reader
	name    string
	isVideo bool
}

func NewStreamReader(name string, r io.Reader, isVideo bool) *StreamReader {
	return &StreamReader{r: r, name: name, isVideo: isVideo}
}

func (s *StreamReader) ReadCodecID() (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(s.r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

func (s *StreamReader) Next() (*MediaPacket, *VideoSession, error) {
	var h [12]byte
	if _, err := io.ReadFull(s.r, h[:]); err != nil {
		return nil, nil, err
	}
	flagsPTS := binary.BigEndian.Uint64(h[:8])
	if flagsPTS&packetFlagSession != 0 {
		if !s.isVideo {
			return nil, nil, fmt.Errorf("%s: unexpected session packet on non-video stream", s.name)
		}
		return nil, &VideoSession{
			Width:  binary.BigEndian.Uint32(h[4:8]),
			Height: binary.BigEndian.Uint32(h[8:12]),
		}, nil
	}

	size := binary.BigEndian.Uint32(h[8:12])
	if size == 0 {
		return nil, nil, fmt.Errorf("%s: zero-size media packet", s.name)
	}
	if size > 4*1024*1024 {
		return nil, nil, fmt.Errorf("%s: media packet too large: %d", s.name, size)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(s.r, data); err != nil {
		return nil, nil, err
	}
	p := &MediaPacket{
		Data:     data,
		Config:   flagsPTS&packetFlagConfig != 0,
		KeyFrame: flagsPTS&packetFlagKeyFrame != 0,
	}
	if !p.Config {
		p.PTS = flagsPTS & packetPTSMask
	}
	return p, nil, nil
}
