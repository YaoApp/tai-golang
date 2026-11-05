package transport

import (
	"math"
	"testing"

	"github.com/yaoapp/tai-golang/types"
)

func TestEncodeDecodeAudioFrame(t *testing.T) {
	pcm := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	channelID := uint32(1)
	seq := uint32(42)

	encoded := EncodeAudioFrame(channelID, seq, pcm)

	gotCh, gotSeq, gotPCM, err := DecodeAudioFrame(encoded)
	if err != nil {
		t.Fatalf("DecodeAudioFrame: %v", err)
	}
	if gotCh != channelID {
		t.Errorf("channelID = %d, want %d", gotCh, channelID)
	}
	if gotSeq != seq {
		t.Errorf("seq = %d, want %d", gotSeq, seq)
	}
	if len(gotPCM) != len(pcm) {
		t.Fatalf("pcm len = %d, want %d", len(gotPCM), len(pcm))
	}
	for i, b := range pcm {
		if gotPCM[i] != b {
			t.Errorf("pcm[%d] = %d, want %d", i, gotPCM[i], b)
		}
	}
}

func TestDecodeAudioFrame_TooShort(t *testing.T) {
	_, _, _, err := DecodeAudioFrame([]byte{0x01, 0x02})
	if err != ErrShortDatagram {
		t.Errorf("err = %v, want ErrShortDatagram", err)
	}
}

func TestDecodeAudioFrame_EmptyData(t *testing.T) {
	_, _, _, err := DecodeAudioFrame(nil)
	if err != ErrShortDatagram {
		t.Errorf("err = %v, want ErrShortDatagram", err)
	}
}

func TestEncodeAudioFrame_EmptyPCM(t *testing.T) {
	encoded := EncodeAudioFrame(3, 7, nil)
	if len(encoded) != datagramHeaderSize {
		t.Fatalf("len = %d, want %d", len(encoded), datagramHeaderSize)
	}
	ch, seq, pcm, err := DecodeAudioFrame(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ch != 3 || seq != 7 {
		t.Errorf("ch=%d seq=%d, want 3/7", ch, seq)
	}
	if len(pcm) != 0 {
		t.Errorf("pcm len = %d, want 0", len(pcm))
	}
}

func TestEncodeAudioFrame_HeaderOnly(t *testing.T) {
	// Exactly 8 bytes with no PCM payload.
	encoded := EncodeAudioFrame(0, 0, nil)
	ch, seq, pcm, err := DecodeAudioFrame(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ch != 0 || seq != 0 || len(pcm) != 0 {
		t.Errorf("unexpected values: ch=%d seq=%d pcm=%d", ch, seq, len(pcm))
	}
}

func TestDecodeAudioFrame_ExactHeader(t *testing.T) {
	// 8 bytes = valid header, zero-length PCM.
	data := make([]byte, datagramHeaderSize)
	data[3] = 5  // channelID = 5
	data[7] = 10 // seq = 10
	ch, seq, pcm, err := DecodeAudioFrame(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ch != 5 || seq != 10 {
		t.Errorf("ch=%d seq=%d, want 5/10", ch, seq)
	}
	if len(pcm) != 0 {
		t.Errorf("pcm len = %d, want 0", len(pcm))
	}
}

func TestEncodeAudioFrame_LargePCM(t *testing.T) {
	pcm := make([]byte, 960) // typical 20ms PCM16 frame
	for i := range pcm {
		pcm[i] = byte(i % 256)
	}
	encoded := EncodeAudioFrame(1, 100, pcm)
	ch, seq, gotPCM, err := DecodeAudioFrame(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ch != 1 || seq != 100 {
		t.Errorf("ch=%d seq=%d", ch, seq)
	}
	if len(gotPCM) != 960 {
		t.Fatalf("pcm len = %d, want 960", len(gotPCM))
	}
	for i, b := range pcm {
		if gotPCM[i] != b {
			t.Errorf("pcm[%d] = %d, want %d", i, gotPCM[i], b)
			break
		}
	}
}

// --- Motion datagram tests ---

func TestEncodeDecodeMotionFrame_Roundtrip(t *testing.T) {
	f := &types.MotionFrame{
		Version:      types.MotionFrameVersion,
		Profile:      types.ProfileARKit52,
		State:        types.StateSpeaking,
		Emotion:      types.EmotionHappy,
		Intensity:    200,
		FrameSeq:     1234,
		AudioSeq:     567890,
		PtsMs:        9999,
		GestureID:    types.GestureWave,
		GesturePhase: 128,
	}
	for i := range f.Face {
		f.Face[i] = uint8(i * 5)
	}
	f.Head = [3]int8{-10, 20, -5}
	f.Gaze = [2]int8{3, -7}
	f.Viseme = [5]uint8{100, 50, 200, 30, 10}
	for i := range f.Body {
		f.Body[i] = uint8(128 + i)
	}

	channelID := uint32(42)
	seq := uint32(7)
	encoded := EncodeMotionFrame(channelID, seq, f)
	if len(encoded) != motionDatagramSize {
		t.Fatalf("encoded size = %d, want %d", len(encoded), motionDatagramSize)
	}

	gotCh, gotSeq, gotFrame, err := DecodeMotionFrame(encoded)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if gotCh != channelID {
		t.Errorf("channelID = %d, want %d", gotCh, channelID)
	}
	if gotSeq != seq {
		t.Errorf("seq = %d, want %d", gotSeq, seq)
	}
	if gotFrame.Version != f.Version || gotFrame.Profile != f.Profile {
		t.Errorf("version/profile mismatch")
	}
	if gotFrame.FrameSeq != f.FrameSeq || gotFrame.AudioSeq != f.AudioSeq {
		t.Errorf("seq fields mismatch: frame=%d/%d audio=%d/%d",
			gotFrame.FrameSeq, f.FrameSeq, gotFrame.AudioSeq, f.AudioSeq)
	}
	if gotFrame.Head != f.Head || gotFrame.Gaze != f.Gaze {
		t.Errorf("head/gaze mismatch")
	}
}

func TestDecodeMotionFrame_TooShort(t *testing.T) {
	for _, size := range []int{0, 8, 50, 94} {
		_, _, _, err := DecodeMotionFrame(make([]byte, size))
		if err != ErrShortDatagram {
			t.Errorf("size %d: err = %v, want ErrShortDatagram", size, err)
		}
	}
}

func TestEncodeDecodeMotionFrame_MaxValues(t *testing.T) {
	f := &types.MotionFrame{
		Version:      types.MotionFrameVersion,
		Profile:      255,
		Intensity:    255,
		FrameSeq:     math.MaxUint16,
		AudioSeq:     math.MaxUint32,
		PtsMs:        math.MaxUint32,
		GesturePhase: 255,
	}
	f.Head = [3]int8{127, -128, 0}
	f.Gaze = [2]int8{-128, 127}
	encoded := EncodeMotionFrame(math.MaxUint32, math.MaxUint32, f)
	gotCh, gotSeq, gotFrame, err := DecodeMotionFrame(encoded)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if gotCh != math.MaxUint32 || gotSeq != math.MaxUint32 {
		t.Errorf("header max values failed")
	}
	if gotFrame.FrameSeq != math.MaxUint16 || gotFrame.AudioSeq != math.MaxUint32 {
		t.Errorf("frame max values failed")
	}
	if gotFrame.Head != f.Head || gotFrame.Gaze != f.Gaze {
		t.Errorf("signed fields mismatch")
	}
}

func TestMotionDatagramSize_Is95(t *testing.T) {
	if motionDatagramSize != 95 {
		t.Errorf("motionDatagramSize = %d, want 95", motionDatagramSize)
	}
}
