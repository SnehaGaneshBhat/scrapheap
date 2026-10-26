package hub

import (
	"time"

	"github.com/scrap-heap/backend/internal/sched"
)

// DeviceInfo describes the worker's advertised capabilities and benchmark.
type DeviceInfo struct {
	UserAgent   string
	Cores       uint32
	Threads     int
	Mock        bool
	TilesPerSec float64
	PerThread   float64
}

// VerifyStatus records whether a result was checked by a configured verifier.
type VerifyStatus uint8

const (
	NotChecked VerifyStatus = iota
	Passed
)

// Collector receives events from the hub loop. Implementations must be fast and non-blocking.
type Collector interface {
	DeviceJoined(time.Time, string, DeviceInfo)
	DeviceLeft(time.Time, string)
	JobStart(time.Time, uint32, int)
	JobDone(time.Time, uint32, string)
	TaskAssigned(time.Time, string, int, uint32, uint16)
	TaskAccepted(time.Time, string, uint32, uint16, float32, VerifyStatus)
	VerifyFailed(time.Time, string, uint32, uint16, int)
	TaskRequeued(time.Time, uint32, uint16, sched.RequeueReason)
	DupDropped(time.Time, uint32, uint16, bool)
}

// NopCollector discards all events.
type NopCollector struct{}

func (NopCollector) DeviceJoined(time.Time, string, DeviceInfo)                            {}
func (NopCollector) DeviceLeft(time.Time, string)                                          {}
func (NopCollector) JobStart(time.Time, uint32, int)                                       {}
func (NopCollector) JobDone(time.Time, uint32, string)                                     {}
func (NopCollector) TaskAssigned(time.Time, string, int, uint32, uint16)                   {}
func (NopCollector) TaskAccepted(time.Time, string, uint32, uint16, float32, VerifyStatus) {}
func (NopCollector) VerifyFailed(time.Time, string, uint32, uint16, int)                   {}
func (NopCollector) TaskRequeued(time.Time, uint32, uint16, sched.RequeueReason)           {}
func (NopCollector) DupDropped(time.Time, uint32, uint16, bool)                            {}
