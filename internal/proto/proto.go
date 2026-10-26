package proto

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

// MessageType identifies a JSON control message.
type MessageType string

const (
	MessageHello       MessageType = "HELLO"
	MessageBenchResult MessageType = "BENCH_RESULT"
	MessageRequestTask MessageType = "REQUEST_TASK"
	MessageHeartbeat   MessageType = "HEARTBEAT"
	MessageAck         MessageType = "ACK"
	MessageJobDone     MessageType = "JOB_DONE"
)

// Message is the JSON control-message envelope. Fields not used by Type are omitted.
type Message struct {
	Type        MessageType `json:"type"`
	DeviceID    string      `json:"deviceId,omitempty"`
	UserAgent   string      `json:"userAgent,omitempty"`
	Cores       uint32      `json:"cores,omitempty"`
	TilesPerSec float64     `json:"tilesPerSec,omitempty"`
	TaskID      uint32      `json:"taskId,omitempty"`
	Attempt     uint32      `json:"attempt,omitempty"`
}

// Task contains render parameters transported in a TASK binary frame.
type Task struct {
	TaskID  uint32
	Attempt uint32
	X       uint16
	Y       uint16
	W       uint16
	H       uint16
	X0      float64
	Y0      float64
	Scale   float64
	MaxIter uint32
}

// Result contains a RESULT header and tightly packed row-major RGBA bytes.
type Result struct {
	TaskID    uint32
	Attempt   uint32
	W         uint16
	H         uint16
	ComputeMs uint32
	RGBA      []byte
}

const (
	FrameTask   byte = 0x01
	FrameResult byte = 0x02

	TaskHeaderSize   = 45
	ResultHeaderSize = 17
)

var (
	ErrMalformedFrame = errors.New("malformed binary frame")
	ErrInvalidMessage = errors.New("invalid control message")
)

// EncodeJSON validates and encodes one JSON control message.
func EncodeJSON(msg Message) ([]byte, error) {
	fields := messageValues(msg)
	if err := validateMessageFields(msg, rawFields(fields)); err != nil {
		return nil, err
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode control message: %w", err)
	}
	return data, nil
}

// DecodeJSON decodes exactly one JSON control message and rejects unknown,
// missing, or type-inappropriate fields.
func DecodeJSON(data []byte) (Message, error) {
	var msg Message
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&msg); err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Message{}, fmt.Errorf("%w: trailing JSON data", ErrInvalidMessage)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Message{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if err := validateMessageFields(msg, fields); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func messageValues(msg Message) map[string]any {
	fields := map[string]any{"type": msg.Type}
	switch msg.Type {
	case MessageHello:
		fields["deviceId"], fields["userAgent"], fields["cores"] = msg.DeviceID, msg.UserAgent, msg.Cores
	case MessageBenchResult:
		fields["tilesPerSec"] = msg.TilesPerSec
	case MessageAck:
		fields["taskId"], fields["attempt"] = msg.TaskID, msg.Attempt
	}
	return fields
}

func rawFields(values map[string]any) map[string]json.RawMessage {
	encoded, _ := json.Marshal(values) // Values are limited to JSON scalar types.
	fields := make(map[string]json.RawMessage)
	_ = json.Unmarshal(encoded, &fields)
	return fields
}

func validateMessageFields(msg Message, fields map[string]json.RawMessage) error {
	allowed := map[string]bool{"type": true}
	require := func(name string) error {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("%w: missing %q", ErrInvalidMessage, name)
		}
		return nil
	}
	allow := func(names ...string) {
		for _, name := range names {
			allowed[name] = true
		}
	}
	switch msg.Type {
	case MessageHello:
		allow("deviceId", "userAgent", "cores")
		for _, name := range []string{"deviceId", "userAgent", "cores"} {
			if err := require(name); err != nil {
				return err
			}
		}
		if msg.DeviceID == "" || msg.Cores == 0 {
			return fmt.Errorf("%w: HELLO requires deviceId and cores > 0", ErrInvalidMessage)
		}
	case MessageBenchResult:
		allow("tilesPerSec")
		if err := require("tilesPerSec"); err != nil {
			return err
		}
		if msg.TilesPerSec <= 0 || math.IsInf(msg.TilesPerSec, 0) || math.IsNaN(msg.TilesPerSec) {
			return fmt.Errorf("%w: tilesPerSec must be finite and > 0", ErrInvalidMessage)
		}
	case MessageRequestTask, MessageHeartbeat, MessageJobDone:
	case MessageAck:
		allow("taskId", "attempt")
		for _, name := range []string{"taskId", "attempt"} {
			if err := require(name); err != nil {
				return err
			}
		}
		if msg.Attempt == 0 {
			return fmt.Errorf("%w: ACK attempt must be > 0", ErrInvalidMessage)
		}
	default:
		return fmt.Errorf("%w: unknown type %q", ErrInvalidMessage, msg.Type)
	}
	for name := range fields {
		if !allowed[name] {
			return fmt.Errorf("%w: field %q is not valid for %s", ErrInvalidMessage, name, msg.Type)
		}
	}
	return nil
}

// EncodeTask encodes a TASK frame using the fixed 45-byte little-endian header.
func EncodeTask(task Task) ([]byte, error) {
	if !validTask(task) {
		return nil, fmt.Errorf("%w: invalid TASK values", ErrMalformedFrame)
	}
	out := make([]byte, TaskHeaderSize)
	out[0] = FrameTask
	binary.LittleEndian.PutUint32(out[1:5], task.TaskID)
	binary.LittleEndian.PutUint32(out[5:9], task.Attempt)
	binary.LittleEndian.PutUint16(out[9:11], task.X)
	binary.LittleEndian.PutUint16(out[11:13], task.Y)
	binary.LittleEndian.PutUint16(out[13:15], task.W)
	binary.LittleEndian.PutUint16(out[15:17], task.H)
	binary.LittleEndian.PutUint64(out[17:25], math.Float64bits(task.X0))
	binary.LittleEndian.PutUint64(out[25:33], math.Float64bits(task.Y0))
	binary.LittleEndian.PutUint64(out[33:41], math.Float64bits(task.Scale))
	binary.LittleEndian.PutUint32(out[41:45], task.MaxIter)
	return out, nil
}

// DecodeTask validates and decodes one complete TASK frame.
func DecodeTask(data []byte) (Task, error) {
	if len(data) != TaskHeaderSize || data[0] != FrameTask {
		return Task{}, fmt.Errorf("%w: TASK must be exactly %d bytes", ErrMalformedFrame, TaskHeaderSize)
	}
	task := Task{
		TaskID: binary.LittleEndian.Uint32(data[1:5]), Attempt: binary.LittleEndian.Uint32(data[5:9]),
		X: binary.LittleEndian.Uint16(data[9:11]), Y: binary.LittleEndian.Uint16(data[11:13]),
		W: binary.LittleEndian.Uint16(data[13:15]), H: binary.LittleEndian.Uint16(data[15:17]),
		X0:      math.Float64frombits(binary.LittleEndian.Uint64(data[17:25])),
		Y0:      math.Float64frombits(binary.LittleEndian.Uint64(data[25:33])),
		Scale:   math.Float64frombits(binary.LittleEndian.Uint64(data[33:41])),
		MaxIter: binary.LittleEndian.Uint32(data[41:45]),
	}
	if !validTask(task) {
		return Task{}, fmt.Errorf("%w: invalid TASK values", ErrMalformedFrame)
	}
	return task, nil
}

func validTask(task Task) bool {
	return task.W != 0 && task.H != 0 && task.Attempt != 0 && task.MaxIter != 0 &&
		finite(task.X0) && finite(task.Y0) && finite(task.Scale) && task.Scale > 0
}

// EncodeResult encodes a RESULT frame and requires exactly W*H*4 pixel bytes.
func EncodeResult(result Result) ([]byte, error) {
	payloadSize, ok := rgbaSize(result.W, result.H)
	if !ok || result.Attempt == 0 || uint64(len(result.RGBA)) != payloadSize {
		return nil, fmt.Errorf("%w: invalid RESULT dimensions, attempt, or RGBA length", ErrMalformedFrame)
	}
	out := make([]byte, ResultHeaderSize+len(result.RGBA))
	out[0] = FrameResult
	binary.LittleEndian.PutUint32(out[1:5], result.TaskID)
	binary.LittleEndian.PutUint32(out[5:9], result.Attempt)
	binary.LittleEndian.PutUint16(out[9:11], result.W)
	binary.LittleEndian.PutUint16(out[11:13], result.H)
	binary.LittleEndian.PutUint32(out[13:17], result.ComputeMs)
	copy(out[ResultHeaderSize:], result.RGBA)
	return out, nil
}

// DecodeResult validates and decodes one complete RESULT frame.
func DecodeResult(data []byte) (Result, error) {
	if len(data) < ResultHeaderSize || data[0] != FrameResult {
		return Result{}, fmt.Errorf("%w: RESULT header is incomplete or has wrong type", ErrMalformedFrame)
	}
	result := Result{
		TaskID: binary.LittleEndian.Uint32(data[1:5]), Attempt: binary.LittleEndian.Uint32(data[5:9]),
		W: binary.LittleEndian.Uint16(data[9:11]), H: binary.LittleEndian.Uint16(data[11:13]),
		ComputeMs: binary.LittleEndian.Uint32(data[13:17]),
	}
	payloadSize, ok := rgbaSize(result.W, result.H)
	if !ok || result.Attempt == 0 || payloadSize != uint64(len(data)-ResultHeaderSize) {
		return Result{}, fmt.Errorf("%w: RESULT payload length does not match dimensions", ErrMalformedFrame)
	}
	result.RGBA = append([]byte(nil), data[ResultHeaderSize:]...)
	return result, nil
}

func rgbaSize(w, h uint16) (uint64, bool) {
	if w == 0 || h == 0 {
		return 0, false
	}
	return uint64(w) * uint64(h) * 4, true
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
