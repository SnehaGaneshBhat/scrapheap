package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/scrap-heap/backend/internal/proto"
)

type session struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	stateMu sync.Mutex
	jobID   uint32
	active  bool
	config  options
	name    string
}

func runDevice(ctx context.Context, cfg options, index int) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	name := deviceName(cfg.name, index)
	conn, _, err := websocket.Dial(ctx, cfg.url, nil)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	s := &session{conn: conn, config: cfg, name: name}
	if err := s.sendJSON(ctx, proto.Message{Type: proto.MessageHello, DeviceID: name, UserAgent: "Scrap Heap mockworker", Cores: uint32(cfg.threads), Threads: cfg.threads, Mock: true}); err != nil {
		return err
	}
	if err := s.readWelcome(ctx); err != nil {
		return err
	}
	benchPixels := gradient(proto.BenchTile())
	benchHash := sha256.Sum256(benchPixels)
	if err := s.sendJSON(ctx, proto.Message{Type: proto.MessageBenchResult, TilesPerSec: cfg.tilesPerSec * float64(cfg.threads), PerThread: cfg.tilesPerSec, Threads: cfg.threads, Hash: hex.EncodeToString(benchHash[:])}); err != nil {
		return err
	}
	go s.heartbeat(ctx)
	log.Printf("mock device connected id=%s threads=%d perThread=%.2f", name, cfg.threads, cfg.tilesPerSec)
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ == websocket.MessageText {
			msg, err := proto.DecodeJSON(data)
			if err != nil {
				return err
			}
			switch msg.Type {
			case proto.MessageJobStarted:
				s.stateMu.Lock()
				s.jobID, s.active = msg.JobID, true
				s.stateMu.Unlock()
				for i := 0; i < cfg.threads; i++ {
					if err := s.request(ctx); err != nil {
						return err
					}
				}
			case proto.MessageJobDone:
				s.stateMu.Lock()
				if s.jobID == msg.JobID {
					s.active = false
				}
				s.stateMu.Unlock()
				log.Printf("mock device observed JOB_DONE id=%s job=%d", name, msg.JobID)
			case proto.MessageWelcome, proto.MessageAck:
			default:
				log.Printf("mock device %s ignored server message %s", name, msg.Type)
			}
		} else if typ == websocket.MessageBinary {
			task, err := proto.DecodeTask(data)
			if err != nil {
				return err
			}
			go s.compute(ctx, task)
		} else {
			return fmt.Errorf("unexpected websocket frame type %v", typ)
		}
	}
}

func (s *session) readWelcome(ctx context.Context) error {
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			return fmt.Errorf("expected WELCOME text frame, got %v", typ)
		}
		msg, err := proto.DecodeJSON(data)
		if err != nil {
			return err
		}
		if msg.Type != proto.MessageWelcome {
			return fmt.Errorf("expected WELCOME, got %s", msg.Type)
		}
		return nil
	}
}

func (s *session) sendJSON(ctx context.Context, msg proto.Message) error {
	data, err := proto.EncodeJSON(msg)
	if err != nil {
		return err
	}
	return s.write(ctx, websocket.MessageText, data)
}

func (s *session) write(ctx context.Context, typ websocket.MessageType, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(ctx, typ, data)
}

func (s *session) request(ctx context.Context) error {
	return s.sendJSON(ctx, proto.Message{Type: proto.MessageRequestTask})
}

func (s *session) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.sendJSON(ctx, proto.Message{Type: proto.MessageHeartbeat}); err != nil {
				return
			}
		}
	}
}

func (s *session) compute(ctx context.Context, task proto.Task) {
	duration := time.Duration(float64(time.Second) / s.config.tilesPerSec * float64(task.W) * float64(task.H) / float64(proto.TileSize*proto.TileSize))
	if s.config.jitter > 0 {
		variation := (rand.Float64()*2 - 1) * s.config.jitter
		duration = time.Duration(float64(duration) * (1 + variation))
	}
	if duration < 0 {
		duration = 0
	}
	timer := time.NewTimer(duration)
	select {
	case <-ctx.Done():
		timer.Stop()
		return
	case <-timer.C:
	}
	pixels := gradient(task)
	result, err := proto.EncodeResult(proto.Result{JobID: task.JobID, TaskID: task.TaskID, Attempt: task.Attempt, W: task.W, H: task.H, ComputeMs: float32(duration.Seconds() * 1000), RGBA: pixels})
	if err != nil {
		log.Printf("mock device %s encode RESULT: %v", s.name, err)
		return
	}
	if err := s.write(ctx, websocket.MessageBinary, result); err != nil {
		return
	}
	s.stateMu.Lock()
	requestNext := s.active && s.jobID == task.JobID
	s.stateMu.Unlock()
	if requestNext {
		_ = s.request(ctx)
	}
}

func gradient(task proto.Task) []byte {
	pixels := make([]byte, int(task.W)*int(task.H)*4)
	for py := 0; py < int(task.H); py++ {
		for px := 0; px < int(task.W); px++ {
			offset := (py*int(task.W) + px) * 4
			pixels[offset] = byte((int(task.X) + px) % 256)
			pixels[offset+1] = byte((int(task.Y) + py) % 256)
			pixels[offset+2] = byte((px + py) % 256)
			pixels[offset+3] = 255
		}
	}
	return pixels
}
