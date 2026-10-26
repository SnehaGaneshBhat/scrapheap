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
	MessageWelcome     MessageType = "WELCOME"
)

// Message is the JSON control-message envelope. Fields not used by Type are omitted.
type Message struct {
	Type                MessageType `json:"type"`
	DeviceID            string      `json:"deviceId,omitempty"`
	UserAgent           string      `json:"userAgent,omitempty"`
	Cores               uint32      `json:"cores,omitempty"`
	TilesPerSec         float64     `json:"tilesPerSec,omitempty"`
	TaskID              uint32      `json:"taskId,omitempty"`
	Attempt             uint32      `json:"attempt,omitempty"`
	JobID               uint32      `json:"jobId,omitempty"`
	HeartbeatIntervalMs uint32      `json:"heartbeatIntervalMs,omitempty"`
	HeartbeatTimeoutMs  uint32      `json:"heartbeatTimeoutMs,omitempty"`
}

// Task contains render parameters transported in a TASK binary frame.
type Task struct {
	JobID   uint32
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
	JobID     uint32
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

	MaxTileDim       = 256
	TaskHeaderSize   = 49
	ResultHeaderSize = 21
)

var (
	ErrMalformedFrame = errors.New("malformed binary frame")
	ErrInvalidMessage = errors.New("invalid control message")
)

// UnknownMessageTypeError identifies an unsupported JSON control-message type.
type UnknownMessageTypeError struct{ Type string }

func (e *UnknownMessageTypeError) Error() string {
	return fmt.Sprintf("unknown control message type %q", e.Type)
}

// UnknownFrameTypeError identifies an unsupported binary frame discriminator.
type UnknownFrameTypeError struct{ Type byte }

func (e *UnknownFrameTypeError) Error() string {
	return fmt.Sprintf("unknown binary frame type 0x%02x", e.Type)
}

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
		fields["taskId"], fields["attempt"], fields["jobId"] = msg.TaskID, msg.Attempt, msg.JobID
	case MessageJobDone:
		fields["jobId"] = msg.JobID
	case MessageWelcome:
		fields["heartbeatIntervalMs"], fields["heartbeatTimeoutMs"] = msg.HeartbeatIntervalMs, msg.HeartbeatTimeoutMs
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
		if msg.Type == MessageJobDone {
			allow("jobId")
			if err := require("jobId"); err != nil {
				return err
			}
		}
	case MessageAck:
		allow("taskId", "attempt", "jobId")
		for _, name := range []string{"taskId", "attempt", "jobId"} {
			if err := require(name); err != nil {
				return err
			}
		}
		if msg.Attempt == 0 {
			return fmt.Errorf("%w: ACK attempt must be > 0", ErrInvalidMessage)
		}
	case MessageWelcome:
		allow("heartbeatIntervalMs", "heartbeatTimeoutMs")
		for _, name := range []string{"heartbeatIntervalMs", "heartbeatTimeoutMs"} {
			if err := require(name); err != nil {
				return err
			}
		}
		if msg.HeartbeatIntervalMs == 0 || msg.HeartbeatTimeoutMs == 0 {
			return fmt.Errorf("%w: WELCOME heartbeat intervals must be > 0", ErrInvalidMessage)
		}
	default:
		return &UnknownMessageTypeError{Type: string(msg.Type)}
	}
	for name := range fields {
		if !allowed[name] {
			return fmt.Errorf("%w: field %q is not valid for %s", ErrInvalidMessage, name, msg.Type)
		}
	}
	return nil
}

// EncodeTask encodes a TASK frame using the fixed 49-byte little-endian header.
func EncodeTask(task Task) ([]byte, error) {
	if !validTask(task) {
		return nil, fmt.Errorf("%w: invalid TASK values", ErrMalformedFrame)
	}
	out := make([]byte, TaskHeaderSize)
	out[0] = FrameTask
	binary.LittleEndian.PutUint32(out[1:5], task.JobID)
	binary.LittleEndian.PutUint32(out[5:9], task.TaskID)
	binary.LittleEndian.PutUint32(out[9:13], task.Attempt)
	binary.LittleEndian.PutUint16(out[13:15], task.X)
	binary.LittleEndian.PutUint16(out[15:17], task.Y)
	binary.LittleEndian.PutUint16(out[17:19], task.W)
	binary.LittleEndian.PutUint16(out[19:21], task.H)
	binary.LittleEndian.PutUint64(out[21:29], math.Float64bits(task.X0))
	binary.LittleEndian.PutUint64(out[29:37], math.Float64bits(task.Y0))
	binary.LittleEndian.PutUint64(out[37:45], math.Float64bits(task.Scale))
	binary.LittleEndian.PutUint32(out[45:49], task.MaxIter)
	return out, nil
}

// DecodeTask validates and decodes one complete TASK frame.
func DecodeTask(data []byte) (Task, error) {
	if len(data) == 0 {
		return Task{}, fmt.Errorf("%w: TASK frame is empty", ErrMalformedFrame)
	}
	if data[0] != FrameTask && data[0] != FrameResult {
		return Task{}, &UnknownFrameTypeError{Type: data[0]}
	}
	if len(data) != TaskHeaderSize || data[0] != FrameTask {
		return Task{}, fmt.Errorf("%w: TASK must be exactly %d bytes", ErrMalformedFrame, TaskHeaderSize)
	}
	task := Task{
		JobID:  binary.LittleEndian.Uint32(data[1:5]),
		TaskID: binary.LittleEndian.Uint32(data[5:9]), Attempt: binary.LittleEndian.Uint32(data[9:13]),
		X: binary.LittleEndian.Uint16(data[13:15]), Y: binary.LittleEndian.Uint16(data[15:17]),
		W: binary.LittleEndian.Uint16(data[17:19]), H: binary.LittleEndian.Uint16(data[19:21]),
		X0:      math.Float64frombits(binary.LittleEndian.Uint64(data[21:29])),
		Y0:      math.Float64frombits(binary.LittleEndian.Uint64(data[29:37])),
		Scale:   math.Float64frombits(binary.LittleEndian.Uint64(data[37:45])),
		MaxIter: binary.LittleEndian.Uint32(data[45:49]),
	}
	if !validTask(task) {
		return Task{}, fmt.Errorf("%w: invalid TASK values", ErrMalformedFrame)
	}
	return task, nil
}

func validTask(task Task) bool {
	return validTileDims(task.W, task.H) && task.Attempt != 0 && task.MaxIter != 0 &&
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
	binary.LittleEndian.PutUint32(out[1:5], result.JobID)
	binary.LittleEndian.PutUint32(out[5:9], result.TaskID)
	binary.LittleEndian.PutUint32(out[9:13], result.Attempt)
	binary.LittleEndian.PutUint16(out[13:15], result.W)
	binary.LittleEndian.PutUint16(out[15:17], result.H)
	binary.LittleEndian.PutUint32(out[17:21], result.ComputeMs)
	copy(out[ResultHeaderSize:], result.RGBA)
	return out, nil
}

// DecodeResult validates and decodes one complete RESULT frame.
func DecodeResult(data []byte) (Result, error) {
	if len(data) == 0 {
		return Result{}, fmt.Errorf("%w: RESULT frame is empty", ErrMalformedFrame)
	}
	if data[0] != FrameTask && data[0] != FrameResult {
		return Result{}, &UnknownFrameTypeError{Type: data[0]}
	}
	if len(data) < ResultHeaderSize || data[0] != FrameResult {
		return Result{}, fmt.Errorf("%w: RESULT header is incomplete or has wrong type", ErrMalformedFrame)
	}
	result := Result{
		JobID:  binary.LittleEndian.Uint32(data[1:5]),
		TaskID: binary.LittleEndian.Uint32(data[5:9]), Attempt: binary.LittleEndian.Uint32(data[9:13]),
		W: binary.LittleEndian.Uint16(data[13:15]), H: binary.LittleEndian.Uint16(data[15:17]),
		ComputeMs: binary.LittleEndian.Uint32(data[17:21]),
	}
	payloadSize, ok := rgbaSize(result.W, result.H)
	if !ok || result.Attempt == 0 || payloadSize != uint64(len(data)-ResultHeaderSize) {
		return Result{}, fmt.Errorf("%w: RESULT payload length does not match dimensions", ErrMalformedFrame)
	}
	result.RGBA = append([]byte(nil), data[ResultHeaderSize:]...)
	return result, nil
}

func rgbaSize(w, h uint16) (uint64, bool) {
	if !validTileDims(w, h) {
		return 0, false
	}
	width, height := uint64(w), uint64(h)
	if width > math.MaxUint64/height/4 {
		return 0, false
	}
	return width * height * 4, true
}

func validTileDims(w, h uint16) bool { return w > 0 && h > 0 && w <= MaxTileDim && h <= MaxTileDim }

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
