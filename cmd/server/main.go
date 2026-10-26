package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/scrap-heap/backend/internal/hub"
	"github.com/scrap-heap/backend/internal/sched"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	webDir := flag.String("web", "web", "static web directory")
	autostart := flag.Bool("autostart", true, "start a render job at boot")
	x0 := flag.Float64("x0", -2.5, "Mandelbrot view x origin")
	y0 := flag.Float64("y0", -1.75, "Mandelbrot view y origin")
	scale := flag.Float64("scale", 3.5/2048, "Mandelbrot view scale per pixel")
	maxIter := flag.Uint("maxiter", 1000, "maximum Mandelbrot iterations")
	benchHash := flag.String("bench-hash", "", "expected benchmark output SHA-256 (empty accepts and logs all hashes)")
	heartbeatTimeout := flag.Duration("heartbeat-timeout", 5*time.Second, "worker heartbeat timeout")
	modeName := flag.String("mode", "aware", "scheduler policy: pull, plain (alias for pull), or aware")
	publicHost := flag.String("host", "", "public host name or IP used in displayed URLs and snapshots")
	verbose := flag.Bool("verbose", false, "log per-task activity")
	flag.Parse()

	mode, err := parseSchedulerMode(*modeName)
	if err != nil {
		log.Fatal(err)
	}
	if math.IsNaN(*x0) || math.IsInf(*x0, 0) || math.IsNaN(*y0) || math.IsInf(*y0, 0) || math.IsNaN(*scale) || math.IsInf(*scale, 0) || *scale <= 0 || *maxIter == 0 || uint64(*maxIter) > uint64(^uint32(0)) {
		log.Fatal("view coordinates and scale must be finite, scale and maxiter must be positive, and maxiter must fit uint32")
	}
	if *heartbeatTimeout <= 0 {
		log.Fatal("-heartbeat-timeout must be positive")
	}
	if *benchHash != "" {
		decoded, err := hex.DecodeString(*benchHash)
		if err != nil || len(decoded) != 32 {
			log.Fatal("-bench-hash must be a 64-character SHA-256 hex digest")
		}
	}
	config := hub.Config{
		Mode:              mode,
		Job:               hub.JobConfig{X0: *x0, Y0: *y0, Scale: *scale, MaxIter: uint32(*maxIter), CanvasSize: 2048},
		ExpectedBenchHash: *benchHash,
		HeartbeatTimeout:  *heartbeatTimeout,
		PublicHost:        *publicHost,
	}
	workerHub := hub.New(config, newLogCollector(*verbose), nil)
	root, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go workerHub.Run(root)
	if *autostart {
		log.Printf("started initial job id=%d", workerHub.StartNewJob())
	}

	mux := newHTTPHandler(workerHub, *webDir)
	// TODO: RegisterDash and RegisterQR when the teammate-owned dashws.go and qr.go exist.
	server := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	printURLs(listener.Addr(), *publicHost)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case <-root.Done():
	case err = <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server: %v", err)
		}
		stop()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}
}

func parseSchedulerMode(value string) (sched.Mode, error) {
	switch strings.ToLower(value) {
	case "aware":
		return sched.CapabilityAware, nil
	case "pull", "plain":
		return sched.PlainPull, nil
	default:
		return sched.PlainPull, fmt.Errorf("invalid -mode %q: expected pull, plain, or aware", value)
	}
}

func printURLs(addr net.Addr, publicHost string) {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return
	}
	if publicHost != "" {
		printURLPair(strings.Trim(publicHost, "[]"), tcp.Port)
		return
	}
	found := false
	interfaces, err := net.Interfaces()
	if err != nil {
		log.Printf("list network interfaces: %v", err)
		return
	}
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch value := addr.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			if !isPrintableIPv4(ip) {
				continue
			}
			printURLPair(ip.To4().String(), tcp.Port)
			found = true
		}
	}
	if !found {
		log.Printf("no non-loopback IPv4 addresses found; listening on %s", addr)
	}
}

func isPrintableIPv4(ip net.IP) bool {
	ipv4 := ip.To4()
	return ipv4 != nil && !ipv4.IsLoopback() && !ipv4.IsLinkLocalUnicast()
}

func printURLPair(host string, port int) {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	fmt.Printf("Worker URL:    http://%s/worker\nDashboard URL: http://%s/dash\n", address, address)
}
