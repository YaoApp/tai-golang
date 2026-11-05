package transport

import (
	"context"
	"encoding/binary"
	"errors"

	"github.com/yaoapp/tai-golang/types"
)

// Datagram frame format (05-channels-realtime.md):
//
//	channel_id (4 bytes, big-endian) + seq (4 bytes, big-endian) + pcm_data (N bytes)
const datagramHeaderSize = 8

// ErrShortDatagram is returned when a datagram is too small to contain a valid header.
var ErrShortDatagram = errors.New("datagram too short")

// ErrDatagramNotSupported is returned by H2 connections that lack QUIC datagram capability.
var ErrDatagramNotSupported = errors.New("datagram not supported on H2 transport")

// DatagramConn abstracts QUIC datagram send/receive so session code
// works identically over H3 (real datagrams) and H2 (bidi stream fallback).
type DatagramConn interface {
	SendDatagram(data []byte) error
	ReceiveDatagram(ctx context.Context) ([]byte, error)
	Proto() string // "H3" or "H2"
}

// EncodeAudioFrame encodes a PCM audio frame into the datagram wire format.
func EncodeAudioFrame(channelID, seq uint32, pcm []byte) []byte {
	buf := make([]byte, datagramHeaderSize+len(pcm))
	binary.BigEndian.PutUint32(buf[0:4], channelID)
	binary.BigEndian.PutUint32(buf[4:8], seq)
	copy(buf[datagramHeaderSize:], pcm)
	return buf
}

// DecodeAudioFrame decodes a datagram wire frame into its components.
func DecodeAudioFrame(data []byte) (channelID, seq uint32, pcm []byte, err error) {
	if len(data) < datagramHeaderSize {
		return 0, 0, nil, ErrShortDatagram
	}
	channelID = binary.BigEndian.Uint32(data[0:4])
	seq = binary.BigEndian.Uint32(data[4:8])
	pcm = data[datagramHeaderSize:]
	return channelID, seq, pcm, nil
}

// motionDatagramSize is the total size of a motion datagram: 8-byte header + 87-byte MotionFrame.
const motionDatagramSize = datagramHeaderSize + types.MotionFrameSize // 95

// EncodeMotionFrame encodes a MotionFrame into the datagram wire format (95 bytes).
func EncodeMotionFrame(channelID, seq uint32, frame *types.MotionFrame) []byte {
	buf := make([]byte, motionDatagramSize)
	binary.BigEndian.PutUint32(buf[0:4], channelID)
	binary.BigEndian.PutUint32(buf[4:8], seq)
	packed := frame.Pack()
	copy(buf[datagramHeaderSize:], packed[:])
	return buf
}

// DecodeMotionFrame decodes a motion datagram into its components.
func DecodeMotionFrame(data []byte) (channelID, seq uint32, frame *types.MotionFrame, err error) {
	if len(data) < motionDatagramSize {
		return 0, 0, nil, ErrShortDatagram
	}
	channelID = binary.BigEndian.Uint32(data[0:4])
	seq = binary.BigEndian.Uint32(data[4:8])
	frame, err = types.UnpackMotionFrame(data[datagramHeaderSize:])
	if err != nil {
		return 0, 0, nil, err
	}
	return channelID, seq, frame, nil
}
