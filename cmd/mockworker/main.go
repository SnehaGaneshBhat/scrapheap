package main

import (
	"context"
	"flag"
	"log"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type options struct {
	url         string
	devices     int
	threads     int
	tilesPerSec float64
	jitter      float64
	killAfter   time.Duration
	name        string
}

func main() {
	var cfg options
	flag.StringVar(&cfg.url, "url", "ws://localhost:8080/ws/worker", "worker WebSocket URL")
	flag.IntVar(&cfg.devices, "devices", 3, "number of mock devices")
	flag.IntVar(&cfg.threads, "threads", 2, "worker threads per device")
	flag.Float64Var(&cfg.tilesPerSec, "tiles-per-sec", 20, "simulated per-thread tiles per second")
	flag.Float64Var(&cfg.jitter, "jitter", 0, "random compute-time variation from 0 to 1")
	flag.DurationVar(&cfg.killAfter, "kill-after", 0, "disconnect the first device after this duration")
	flag.StringVar(&cfg.name, "name", "mock", "device ID prefix")
	flag.Parse()
	if cfg.devices < 1 || cfg.threads < 1 || cfg.threads > 16 || cfg.tilesPerSec <= 0 || math.IsNaN(cfg.tilesPerSec) || math.IsInf(cfg.tilesPerSec, 0) || cfg.jitter < 0 || cfg.jitter > 1 || cfg.killAfter < 0 {
		log.Fatal("invalid options: devices >= 1, threads in 1..16, tiles-per-sec > 0, jitter in 0..1, kill-after >= 0 required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < cfg.devices; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			deviceCtx := ctx
			deviceCancel := func() {}
			if index == 0 && cfg.killAfter > 0 {
				deviceCtx, deviceCancel = context.WithCancel(ctx)
				go func() {
					timer := time.NewTimer(cfg.killAfter)
					defer timer.Stop()
					select {
					case <-timer.C:
						log.Printf("device %s disconnecting after %s", deviceName(cfg.name, index), cfg.killAfter)
						deviceCancel()
					case <-deviceCtx.Done():
					}
				}()
			}
			defer deviceCancel()
			if err := runDevice(deviceCtx, cfg, index); err != nil && ctx.Err() == nil {
				log.Printf("device %s stopped: %v", deviceName(cfg.name, index), err)
			}
		}(i)
	}
	wg.Wait()
}

func deviceName(prefix string, index int) string {
	return strings.TrimSpace(prefix) + "-" + strconv.Itoa(index+1)
}
