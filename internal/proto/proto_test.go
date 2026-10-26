package proto

import (
	"bytes"
	"encoding/json"
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
		{"ack", Message{Type: MessageAck, TaskID: 0, Attempt: 3}},
		{"job done", Message{Type: MessageJobDone}},
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

func TestJSONMalformed(t *testing.T) {
	tests := []struct{ name, input string }{
		{"invalid json", `{`},
		{"unknown type", `{"type":"NOPE"}`},
		{"missing hello field", `{"type":"HELLO","deviceId":"d","cores":1}`},
		{"wrong message field", `{"type":"HEARTBEAT","cores":2}`},
		{"unknown field", `{"type":"REQUEST_TASK","extra":1}`},
		{"zero cores", `{"type":"HELLO","deviceId":"d","userAgent":"ua","cores":0}`},
		{"trailing json", `{"type":"JOB_DONE"}{}`},
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
	tests := []struct {
		name   string
		encode func() ([]byte, error)
		decode func([]byte) (any, error)
		want   any
	}{
		{"TASK", func() ([]byte, error) {
			return EncodeTask(Task{TaskID: 0x01020304, Attempt: 2, X: 10, Y: 20, W: 2, H: 1, X0: -2, Y0: 0.5, Scale: 1, MaxIter: 256})
		}, func(b []byte) (any, error) { return DecodeTask(b) }, Task{TaskID: 0x01020304, Attempt: 2, X: 10, Y: 20, W: 2, H: 1, X0: -2, Y0: 0.5, Scale: 1, MaxIter: 256}},
		{"RESULT", func() ([]byte, error) {
			return EncodeResult(Result{TaskID: 0x01020304, Attempt: 2, W: 2, H: 1, ComputeMs: 300, RGBA: []byte{0, 1, 2, 255, 16, 17, 18, 255}})
		}, func(b []byte) (any, error) { return DecodeResult(b) }, Result{TaskID: 0x01020304, Attempt: 2, W: 2, H: 1, ComputeMs: 300, RGBA: []byte{0, 1, 2, 255, 16, 17, 18, 255}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := tt.encode()
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := tt.decode(encoded)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			switch want := tt.want.(type) {
			case Task:
				if got.(Task) != want {
					t.Fatalf("got %#v, want %#v", got, want)
				}
			case Result:
				actual := got.(Result)
				if actual.TaskID != want.TaskID || actual.Attempt != want.Attempt || actual.W != want.W || actual.H != want.H || actual.ComputeMs != want.ComputeMs || !bytes.Equal(actual.RGBA, want.RGBA) {
					t.Fatalf("got %#v, want %#v", actual, want)
				}
			}
		})
	}
}

func TestBinaryMalformed(t *testing.T) {
	goodTask, _ := EncodeTask(Task{TaskID: 1, Attempt: 1, W: 1, H: 1, X0: -2, Y0: 0, Scale: 1, MaxIter: 10})
	goodResult, _ := EncodeResult(Result{TaskID: 1, Attempt: 1, W: 1, H: 1, RGBA: []byte{1, 2, 3, 4}})
	badTaskValue := append([]byte(nil), goodTask...)
	badTaskValue[17] = 0 // replace x0 bits with +0 (valid), then invalidate scale below
	badTaskValue[33] = 0
	badTaskValue[34] = 0
	badTaskValue[35] = 0
	badTaskValue[36] = 0
	badTaskValue[37] = 0
	badTaskValue[38] = 0
	badTaskValue[39] = 0
	badTaskValue[40] = 0
	badTaskValue[41] = 0
	badTaskValue[42] = 0
	badTaskValue[43] = 0
	badTaskValue[44] = 0
	badResultPayload := append(append([]byte(nil), goodResult...), 5)
	badResultDims := append([]byte(nil), goodResult...)
	badResultDims[9], badResultDims[10] = 0, 0
	nanTask := append([]byte(nil), goodTask...)
	for i := 17; i < 25; i++ {
		nanTask[i] = 0
	}
	for i := 0; i < 8; i++ {
		nanTask[17+i] = byte(math.Float64bits(math.NaN()) >> (8 * i))
	}

	tests := []struct {
		name   string
		data   []byte
		decode func([]byte) error
	}{
		{"empty task", nil, func(b []byte) error { _, err := DecodeTask(b); return err }},
		{"short task", goodTask[:len(goodTask)-1], func(b []byte) error { _, err := DecodeTask(b); return err }},
		{"wrong task type", append([]byte{FrameResult}, goodTask[1:]...), func(b []byte) error { _, err := DecodeTask(b); return err }},
		{"zero task width", func() []byte { b := append([]byte(nil), goodTask...); b[13], b[14] = 0, 0; return b }(), func(b []byte) error { _, err := DecodeTask(b); return err }},
		{"zero task scale", badTaskValue, func(b []byte) error { _, err := DecodeTask(b); return err }},
		{"nonfinite task coordinate", nanTask, func(b []byte) error { _, err := DecodeTask(b); return err }},
		{"short result header", goodResult[:ResultHeaderSize-1], func(b []byte) error { _, err := DecodeResult(b); return err }},
		{"wrong result type", append([]byte{FrameTask}, goodResult[1:]...), func(b []byte) error { _, err := DecodeResult(b); return err }},
		{"extra result pixel", badResultPayload, func(b []byte) error { _, err := DecodeResult(b); return err }},
		{"zero result width", badResultDims, func(b []byte) error { _, err := DecodeResult(b); return err }},
		{"truncated result", goodResult[:len(goodResult)-1], func(b []byte) error { _, err := DecodeResult(b); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.decode(tt.data); err == nil {
				t.Fatal("decode unexpectedly succeeded")
			}
		})
	}
}

func TestEncodeRejectsInvalidValues(t *testing.T) {
	if _, err := EncodeTask(Task{W: 1, H: 1, Attempt: 1, Scale: math.Inf(1), MaxIter: 1}); err == nil {
		t.Fatal("EncodeTask accepted infinity")
	}
	if _, err := EncodeResult(Result{Attempt: 1, W: 1, H: 1, RGBA: []byte{1, 2, 3}}); err == nil {
		t.Fatal("EncodeResult accepted wrong pixel length")
	}
	if _, err := EncodeJSON(Message{Type: MessageBenchResult, TilesPerSec: math.NaN()}); err == nil {
		t.Fatal("EncodeJSON accepted NaN")
	}
}

func TestJSONFormat(t *testing.T) {
	data, err := EncodeJSON(Message{Type: MessageHello, DeviceID: "d", UserAgent: "", Cores: 1})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["userAgent"] != "" {
		t.Fatalf("empty userAgent was not preserved: %s", data)
	}
}
