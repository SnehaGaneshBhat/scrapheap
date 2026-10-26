package main

import (
	"log"
	"sync"
	"time"

	"github.com/scrap-heap/backend/internal/hub"
	"github.com/scrap-heap/backend/internal/sched"
)

type logCollector struct {
	verbose bool
	mu      sync.Mutex
	started map[uint32]time.Time
}

func newLogCollector(verbose bool) *logCollector {
	return &logCollector{verbose: verbose, started: make(map[uint32]time.Time)}
}

func (c *logCollector) DeviceJoined(now time.Time, id string, info hub.DeviceInfo) {
	log.Printf("device joined id=%s threads=%d mock=%t perThread=%.3f tilesPerSec=%.3f", id, info.Threads, info.Mock, info.PerThread, info.TilesPerSec)
}
func (c *logCollector) DeviceLeft(_ time.Time, id string) { log.Printf("device left id=%s", id) }
func (c *logCollector) JobStart(now time.Time, jobID uint32, tiles int) {
	c.mu.Lock()
	c.started[jobID] = now
	c.mu.Unlock()
	log.Printf("job started id=%d tiles=%d", jobID, tiles)
}
func (c *logCollector) JobDone(now time.Time, jobID uint32, imageHash string) {
	c.mu.Lock()
	start := c.started[jobID]
	c.mu.Unlock()
	log.Printf("job done id=%d imageHash=%s wall=%s", jobID, imageHash, now.Sub(start).Round(time.Millisecond))
}
func (c *logCollector) TaskAssigned(_ time.Time, deviceID string, threadID int, taskID uint32, attempt uint16) {
	if c.verbose {
		log.Printf("task assigned device=%s thread=%d task=%d attempt=%d", deviceID, threadID, taskID, attempt)
	}
}
func (c *logCollector) TaskAccepted(_ time.Time, deviceID string, taskID uint32, attempt uint16, computeMs float32, verified hub.VerifyStatus) {
	if c.verbose {
		log.Printf("task accepted device=%s task=%d attempt=%d computeMs=%.3f verified=%d", deviceID, taskID, attempt, computeMs, verified)
	}
}
func (c *logCollector) VerifyFailed(_ time.Time, deviceID string, taskID uint32, attempt uint16, mismatched int) {
	log.Printf("verification failed device=%s task=%d attempt=%d mismatched=%d", deviceID, taskID, attempt, mismatched)
}
func (c *logCollector) TaskRequeued(_ time.Time, taskID uint32, attempt uint16, reason sched.RequeueReason) {
	log.Printf("task requeued task=%d attempt=%d reason=%s", taskID, attempt, reason)
}
func (c *logCollector) DupDropped(_ time.Time, taskID uint32, attempt uint16, stale bool) {
	if c.verbose {
		log.Printf("result dropped task=%d attempt=%d stale=%t", taskID, attempt, stale)
	}
}

var _ hub.Collector = (*logCollector)(nil)
