package proto

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
	}{
		{"hello", Message{Type: MessageHello, DeviceID: "phone-1", UserAgent: "test browser", Cores: 4}},
		{"bench result", Message{Type: MessageBenchResult, TilesPerSec: 12.5}},
		{"request task", Message{Type: MessageRequestTask}},
		{"heartbeat", Message{Type: MessageHeartbeat}},
		{"ack", Message{Type: MessageAck, TaskID: 7, Attempt: 3, JobID: 19}},
		{"job done", Message{Type: MessageJobDone, JobID: 19}},
		{"welcome", Message{Type: MessageWelcome, HeartbeatIntervalMs: 1000, HeartbeatTimeoutMs: 5000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := EncodeJSON(tt.msg)
			if err != nil {
				t.Fatalf("EncodeJSON: %v", err)
			}
			got, err := DecodeJSON(encoded)
			if err != nil {
				t.Fatalf("DecodeJSON: %v", err)
			}
			if got != tt.msg {
				t.Fatalf("round trip = %#v, want %#v", got, tt.msg)
			}
		})
	}
}

func TestUnknownJSONMessageTypeIsTyped(t *testing.T) {
	_, err := DecodeJSON([]byte(`{"type":"UNKNOWN"}`))
	var unknown *UnknownMessageTypeError
	if !errors.As(err, &unknown) || unknown.Type != "UNKNOWN" {
		t.Fatalf("DecodeJSON error = %#v, want UnknownMessageTypeError for UNKNOWN", err)
	}
	_, err = EncodeJSON(Message{Type: "UNKNOWN"})
	if !errors.As(err, &unknown) {
		t.Fatalf("EncodeJSON error = %#v, want UnknownMessageTypeError", err)
	}
}

func TestUnknownBinaryFrameTypeIsTyped(t *testing.T) {
	_, err := DecodeTask([]byte{0x7f})
	var unknown *UnknownFrameTypeError
	if !errors.As(err, &unknown) || unknown.Type != 0x7f {
		t.Fatalf("DecodeTask error = %#v, want UnknownFrameTypeError for 0x7f", err)
	}
}

func TestJSONMalformed(t *testing.T) {
	tests := []struct{ name, input string }{
		{"invalid json", `{`},
		{"missing hello field", `{"type":"HELLO","deviceId":"d","cores":1}`},
		{"wrong message field", `{"type":"HEARTBEAT","cores":2}`},
		{"unknown field", `{"type":"REQUEST_TASK","extra":1}`},
		{"zero cores", `{"type":"HELLO","deviceId":"d","userAgent":"ua","cores":0}`},
		{"ack missing job id", `{"type":"ACK","taskId":7,"attempt":1}`},
		{"job done missing job id", `{"type":"JOB_DONE"}`},
		{"welcome zero interval", `{"type":"WELCOME","heartbeatIntervalMs":0,"heartbeatTimeoutMs":5000}`},
		{"trailing json", `{"type":"JOB_DONE","jobId":1}{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeJSON([]byte(tt.input)); err == nil {
				t.Fatal("DecodeJSON unexpectedly succeeded")
			}
		})
	}
}

func TestBinaryRoundTrip(t *testing.T) {
	t.Run("TASK", func(t *testing.T) {
		want := Task{JobID: 9, TaskID: 0x01020304, Attempt: 2, X: 10, Y: 20, W: 2, H: 1, X0: -2, Y0: 0.5, Scale: 1, MaxIter: 256}
		encoded, err := EncodeTask(want)
		if err != nil {
			t.Fatal(err)
		}
		expected := []byte{
			0x01, 0x09, 0x00, 0x00, 0x00, 0x04, 0x03, 0x02, 0x01, 0x02, 0x00, 0x00, 0x00,
			0x0a, 0x00, 0x14, 0x00, 0x02, 0x00, 0x01, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xc0,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xe0, 0x3f,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xf0, 0x3f, 0x00, 0x01, 0x00, 0x00,
		}
		if !bytes.Equal(encoded, expected) {
			t.Fatalf("TASK bytes = % x, want % x", encoded, expected)
		}
		if len(encoded) != TaskHeaderSize {
			t.Fatalf("TASK length=%d, want %d", len(encoded), TaskHeaderSize)
		}
		got, err := DecodeTask(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})
	t.Run("RESULT", func(t *testing.T) {
		want := Result{JobID: 9, TaskID: 0x01020304, Attempt: 2, W: 2, H: 1, ComputeMs: 300, RGBA: []byte{0, 1, 2, 255, 16, 17, 18, 255}}
		encoded, err := EncodeResult(want)
		if err != nil {
			t.Fatal(err)
		}
		expected := []byte{0x02, 0x09, 0x00, 0x00, 0x00, 0x04, 0x03, 0x02, 0x01, 0x02, 0x00, 0x00, 0x00, 0x02, 0x00, 0x01, 0x00, 0x2c, 0x01, 0x00, 0x00, 0x00, 0x01, 0x02, 0xff, 0x10, 0x11, 0x12, 0xff}
		if !bytes.Equal(encoded, expected) {
			t.Fatalf("RESULT bytes = % x, want % x", encoded, expected)
		}
		if len(encoded) != ResultHeaderSize+len(want.RGBA) {
			t.Fatalf("RESULT length=%d", len(encoded))
		}
		got, err := DecodeResult(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if got.JobID != want.JobID || got.TaskID != want.TaskID || got.Attempt != want.Attempt || got.W != want.W || got.H != want.H || got.ComputeMs != want.ComputeMs || !bytes.Equal(got.RGBA, want.RGBA) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})
}

func TestStaleJobIDIsPreservedForServerCheck(t *testing.T) {
	const currentJobID = 12
	frame, err := EncodeResult(Result{JobID: 11, TaskID: 3, Attempt: 1, W: 1, H: 1, RGBA: []byte{1, 2, 3, 4}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := DecodeResult(frame)
	if err != nil {
		t.Fatal(err)
	}
	if result.JobID == currentJobID {
		t.Fatalf("expected stale jobId 11, got current jobId %d", currentJobID)
	}
}

func TestBinaryMalformedFrames(t *testing.T) {
	goodTask, _ := EncodeTask(Task{JobID: 1, TaskID: 1, Attempt: 1, W: 1, H: 1, X0: -2, Scale: 1, MaxIter: 10})
	goodResult, _ := EncodeResult(Result{JobID: 1, TaskID: 1, Attempt: 1, W: 1, H: 1, RGBA: []byte{1, 2, 3, 4}})
	taskCase := func(name string, mutate func([]byte) []byte) struct {
		name   string
		frame  []byte
		result bool
	} {
		return struct {
			name   string
			frame  []byte
			result bool
		}{name, mutate(append([]byte(nil), goodTask...)), false}
	}
	resultCase := func(name string, mutate func([]byte) []byte) struct {
		name   string
		frame  []byte
		result bool
	} {
		return struct {
			name   string
			frame  []byte
			result bool
		}{name, mutate(append([]byte(nil), goodResult...)), true}
	}
	tests := []struct {
		name   string
		frame  []byte
		result bool
	}{
		taskCase("truncated TASK", func(b []byte) []byte { return b[:len(b)-1] }),
		taskCase("trailing TASK bytes", func(b []byte) []byte { return append(b, 0) }),
		taskCase("wrong TASK type", func(b []byte) []byte { b[0] = FrameResult; return b }),
		taskCase("zero TASK width", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[17:19], 0); return b }),
		taskCase("zero TASK height", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[19:21], 0); return b }),
		taskCase("oversized TASK width", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[17:19], MaxTileDim+1); return b }),
		taskCase("oversized TASK height", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[19:21], MaxTileDim+1); return b }),
		resultCase("truncated RESULT header", func(b []byte) []byte { return b[:ResultHeaderSize-1] }),
		resultCase("truncated RESULT payload", func(b []byte) []byte { return b[:len(b)-1] }),
		resultCase("extra RESULT payload", func(b []byte) []byte { return append(b, 0) }),
		resultCase("wrong RESULT type", func(b []byte) []byte { b[0] = FrameTask; return b }),
		resultCase("zero RESULT width", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[13:15], 0); return b }),
		resultCase("zero RESULT height", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[15:17], 0); return b }),
		resultCase("oversized RESULT width", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[13:15], MaxTileDim+1); return b }),
		resultCase("oversized RESULT height", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[15:17], MaxTileDim+1); return b }),
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.result {
				_, err = DecodeResult(tt.frame)
			} else {
				_, err = DecodeTask(tt.frame)
			}
			if err == nil {
				t.Fatal("decoder unexpectedly accepted malformed frame")
			}
		})
	}
	if _, err := DecodeTask(nil); err == nil {
		t.Fatal("DecodeTask accepted empty frame")
	}
	if _, err := DecodeResult(nil); err == nil {
		t.Fatal("DecodeResult accepted empty frame")
	}
}

func TestEncodeRejectsInvalidValues(t *testing.T) {
	if _, err := EncodeTask(Task{Attempt: 1, W: 1, H: 1, Scale: math.Inf(1), MaxIter: 1}); err == nil {
		t.Fatal("EncodeTask accepted infinity")
	}
	if _, err := EncodeTask(Task{Attempt: 1, W: MaxTileDim + 1, H: 1, Scale: 1, MaxIter: 1}); err == nil {
		t.Fatal("EncodeTask accepted oversized tile")
	}
	if _, err := EncodeResult(Result{Attempt: 1, W: 1, H: 1, RGBA: []byte{1, 2, 3}}); err == nil {
		t.Fatal("EncodeResult accepted wrong pixel length")
	}
	if _, err := EncodeResult(Result{Attempt: 1, W: MaxTileDim + 1, H: 1, RGBA: make([]byte, (MaxTileDim+1)*4)}); err == nil {
		t.Fatal("EncodeResult accepted oversized tile")
	}
	if _, err := EncodeJSON(Message{Type: MessageBenchResult, TilesPerSec: math.NaN()}); err == nil {
		t.Fatal("EncodeJSON accepted NaN")
	}
}

func TestJSONFormat(t *testing.T) {
	data, err := EncodeJSON(Message{Type: MessageAck, TaskID: 7, Attempt: 2, JobID: 9})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["jobId"] != float64(9) {
		t.Fatalf("ACK jobId missing or wrong: %s", data)
	}
}
