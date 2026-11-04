package transport

import (
	"testing"
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
