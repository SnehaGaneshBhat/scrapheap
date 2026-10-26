package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"scrapheap/internal/ml"
)

type caseResult struct {
	Name      string    `json:"name"`
	Config    ml.Config `json:"config"`
	Result    ml.Result `json:"result"`
	Hash      string    `json:"validationHash"`
	HashScope string    `json:"hashScope"`
}

type runInfo struct {
	Type      string `json:"type"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"goVersion"`
	CPU       string `json:"cpu"`
	NumCPU    int    `json:"numCPU"`
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mlcheck:", err)
		os.Exit(1)
	}
}

func run(output *os.File) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(runInfo{
		Type:      "environment",
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		GoVersion: runtime.Version(),
		CPU:       cpuInfo(),
		NumCPU:    runtime.NumCPU(),
	}); err != nil {
		return fmt.Errorf("write environment: %w", err)
	}

	cases := []struct {
		name   string
		config ml.Config
	}{
		{name: "seed-12345", config: ml.Config{Hidden: 8, LR: 0.1, Epochs: 12, Batch: 8, Seed: 12345}},
		{name: "seed-7", config: ml.Config{Hidden: 6, LR: 0.08, Epochs: 9, Batch: 8, Seed: 7}},
		{name: "seed-2026", config: ml.Config{Hidden: 10, LR: 0.05, Epochs: 7, Batch: 16, Seed: 2026}},
	}
	for _, testCase := range cases {
		result, err := ml.Train(testCase.config, syntheticData())
		if err != nil {
			return fmt.Errorf("train %s: %w", testCase.name, err)
		}
		hash := ml.ValidationHash(result)
		entry := caseResult{
			Name:      testCase.name,
			Config:    testCase.config,
			Result:    result,
			Hash:      hex.EncodeToString(hash[:]),
			HashScope: "valAcc and valLoss float64 bits (big-endian); trainSec excluded",
		}
		if err := encoder.Encode(entry); err != nil {
			return fmt.Errorf("write result %s: %w", testCase.name, err)
		}
	}
	return nil
}

func syntheticData() *ml.Data {
	data := &ml.Data{}
	for class := 0; class < 2; class++ {
		for i := 0; i < 16; i++ {
			pixels := []float64{0, 0}
			pixels[class] = 1
			data.Train = append(data.Train, ml.Example{Pixels: pixels, Label: uint8(class)})
		}
		for i := 0; i < 8; i++ {
			pixels := []float64{0, 0}
			pixels[class] = 1
			data.Validation = append(data.Validation, ml.Example{Pixels: pixels, Label: uint8(class)})
		}
	}
	return data
}

func cpuInfo() string {
	if processor := strings.TrimSpace(os.Getenv("PROCESSOR_IDENTIFIER")); processor != "" {
		return processor
	}

	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return fmt.Sprintf("unavailable (runtime reports %d logical CPUs)", runtime.NumCPU())
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "Hardware") {
			if _, value, ok := strings.Cut(line, ":"); ok {
				return strings.TrimSpace(value)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Sprintf("unavailable (%v; runtime reports %d logical CPUs)", err, runtime.NumCPU())
	}
	return fmt.Sprintf("unavailable (runtime reports %d logical CPUs)", runtime.NumCPU())
}
