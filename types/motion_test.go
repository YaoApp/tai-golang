package types

import (
	"math"
	"testing"
)

func TestMotionFramePackUnpack_Roundtrip(t *testing.T) {
	f := &MotionFrame{
		Version:      MotionFrameVersion,
		Profile:      ProfileARKit52,
		State:        StateSpeaking,
		Emotion:      EmotionHappy,
		Intensity:    200,
		FrameSeq:     1234,
		AudioSeq:     567890,
		PtsMs:        9999,
		GestureID:    GestureWave,
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

	packed := f.Pack()
	if len(packed) != MotionFrameSize {
		t.Fatalf("packed size = %d, want %d", len(packed), MotionFrameSize)
	}

	got, err := UnpackMotionFrame(packed[:])
	if err != nil {
		t.Fatalf("unpack error: %v", err)
	}

	if got.Version != f.Version {
		t.Errorf("Version = %d, want %d", got.Version, f.Version)
	}
	if got.FrameSeq != f.FrameSeq {
		t.Errorf("FrameSeq = %d, want %d", got.FrameSeq, f.FrameSeq)
	}
	if got.AudioSeq != f.AudioSeq {
		t.Errorf("AudioSeq = %d, want %d", got.AudioSeq, f.AudioSeq)
	}
	if got.PtsMs != f.PtsMs {
		t.Errorf("PtsMs = %d, want %d", got.PtsMs, f.PtsMs)
	}
	if got.Face != f.Face {
		t.Errorf("Face mismatch")
	}
	if got.Head != f.Head {
		t.Errorf("Head = %v, want %v", got.Head, f.Head)
	}
	if got.Gaze != f.Gaze {
		t.Errorf("Gaze = %v, want %v", got.Gaze, f.Gaze)
	}
	if got.Viseme != f.Viseme {
		t.Errorf("Viseme = %v, want %v", got.Viseme, f.Viseme)
	}
	if got.Body != f.Body {
		t.Errorf("Body = %v, want %v", got.Body, f.Body)
	}
}

func TestMotionFramePackUnpack_MaxValues(t *testing.T) {
	f := &MotionFrame{
		Version:  MotionFrameVersion,
		FrameSeq: math.MaxUint16,
		AudioSeq: math.MaxUint32,
		PtsMs:    math.MaxUint32,
	}
	f.Head = [3]int8{127, 127, 127}
	f.Gaze = [2]int8{127, 127}

	packed := f.Pack()
	got, err := UnpackMotionFrame(packed[:])
	if err != nil {
		t.Fatalf("unpack error: %v", err)
	}
	if got.FrameSeq != math.MaxUint16 {
		t.Errorf("FrameSeq = %d, want %d", got.FrameSeq, math.MaxUint16)
	}
	if got.AudioSeq != math.MaxUint32 {
		t.Errorf("AudioSeq = %d, want %d", got.AudioSeq, uint32(math.MaxUint32))
	}
}

func TestMotionFramePackUnpack_MinValues(t *testing.T) {
	f := &MotionFrame{Version: MotionFrameVersion}
	f.Head = [3]int8{-128, -128, -128}
	f.Gaze = [2]int8{-128, -128}

	packed := f.Pack()
	got, err := UnpackMotionFrame(packed[:])
	if err != nil {
		t.Fatalf("unpack error: %v", err)
	}
	if got.Head != [3]int8{-128, -128, -128} {
		t.Errorf("Head = %v, want [-128,-128,-128]", got.Head)
	}
}

func TestUnpackMotionFrame_TooShort(t *testing.T) {
	for _, size := range []int{0, 1, 42, 86} {
		_, err := UnpackMotionFrame(make([]byte, size))
		if err != ErrShortMotionFrame {
			t.Errorf("size %d: err = %v, want ErrShortMotionFrame", size, err)
		}
	}
}

func TestUnpackMotionFrame_BadVersion(t *testing.T) {
	data := make([]byte, MotionFrameSize)
	data[0] = 99
	_, err := UnpackMotionFrame(data)
	if err != ErrBadMotionVersion {
		t.Errorf("err = %v, want ErrBadMotionVersion", err)
	}
}

func TestMotionFrameSize_Is87(t *testing.T) {
	if MotionFrameSize != 87 {
		t.Errorf("MotionFrameSize = %d, want 87", MotionFrameSize)
	}
}

func TestConstants(t *testing.T) {
	if ProfileARKit52 != 0 || ProfileLive2D16 != 1 || ProfileViseme15 != 2 || ProfileVendor != 3 {
		t.Error("profile constants mismatch")
	}
	if GestureNone != 0 || GestureNod != 1 || GestureWave != 2 || GestureShake != 3 || GesturePoint != 4 {
		t.Error("gesture constants mismatch")
	}
	if StateIdle != 0 || StateListening != 1 || StateThinking != 2 || StateSpeaking != 3 {
		t.Error("state constants mismatch")
	}
	if EmotionNeutral != 0 || EmotionHappy != 1 || EmotionSurprised != 2 || EmotionConfused != 3 || EmotionConcerned != 4 {
		t.Error("emotion constants mismatch")
	}
}
