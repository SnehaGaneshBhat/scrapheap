package ml

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestTrainLearnsSyntheticClasses(t *testing.T) {
	data := binarySyntheticData(64, 32)
	cfg := Config{Hidden: 8, LR: 0.1, Epochs: 1, Batch: 8, Seed: 12345}

	initial, err := Train(cfg, data)
	if err != nil {
		t.Fatalf("Train baseline: %v", err)
	}
	cfg.Epochs = 30
	trained, err := Train(cfg, data)
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if trained.ValLoss >= initial.ValLoss {
		t.Fatalf("validation loss did not decrease: initial %g, trained %g", initial.ValLoss, trained.ValLoss)
	}
	if trained.ValAcc <= 0.5 {
		t.Fatalf("validation accuracy = %g, want above chance", trained.ValAcc)
	}
	if trained.TrainSec < 0 || math.IsNaN(trained.TrainSec) || math.IsInf(trained.TrainSec, 0) {
		t.Fatalf("invalid training duration %g", trained.TrainSec)
	}
}

func TestTrainDoesNotReadTestSet(t *testing.T) {
	data := binarySyntheticData(8, 4)
	data.Test = []Example{{Pixels: nil, Label: 255}}
	if _, err := Train(Config{Hidden: 4, LR: 0.1, Epochs: 2, Batch: 4, Seed: 7}, data); err != nil {
		t.Fatalf("Train inspected held-back test data: %v", err)
	}
}

func TestTrainMetricsAreStableForSeed(t *testing.T) {
	cfg := Config{Hidden: 8, LR: 0.1, Epochs: 12, Batch: 8, Seed: 12345}
	first, err := Train(cfg, binarySyntheticData(16, 8))
	if err != nil {
		t.Fatalf("first Train: %v", err)
	}
	second, err := Train(cfg, binarySyntheticData(16, 8))
	if err != nil {
		t.Fatalf("second Train: %v", err)
	}
	if first.ValAcc != second.ValAcc || first.ValLoss != second.ValLoss {
		t.Fatalf("validation metrics differ for same seed: %#v != %#v", first, second)
	}
}

func TestTrainRejectsInvalidInputs(t *testing.T) {
	valid := binarySyntheticData(4, 2)
	cases := []struct {
		name string
		cfg  Config
		data *Data
	}{
		{name: "nil data", cfg: Config{Hidden: 4, LR: 0.1, Epochs: 1, Batch: 1}, data: nil},
		{name: "zero hidden", cfg: Config{Hidden: 0, LR: 0.1, Epochs: 1, Batch: 1}, data: valid},
		{name: "invalid learning rate", cfg: Config{Hidden: 4, LR: math.NaN(), Epochs: 1, Batch: 1}, data: valid},
		{name: "zero epochs", cfg: Config{Hidden: 4, LR: 0.1, Epochs: 0, Batch: 1}, data: valid},
		{name: "zero batch", cfg: Config{Hidden: 4, LR: 0.1, Epochs: 1, Batch: 0}, data: valid},
		{name: "model dimensions overflow", cfg: Config{Hidden: int(^uint(0) >> 1), LR: 0.1, Epochs: 1, Batch: 1}, data: valid},
		{name: "empty train", cfg: Config{Hidden: 4, LR: 0.1, Epochs: 1, Batch: 1}, data: &Data{Validation: valid.Validation}},
		{name: "empty validation", cfg: Config{Hidden: 4, LR: 0.1, Epochs: 1, Batch: 1}, data: &Data{Train: valid.Train}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Train(test.cfg, test.data); err == nil {
				t.Fatal("Train unexpectedly succeeded")
			}
		})
	}
}

func TestTrainBatchLargerThanDataset(t *testing.T) {
	cfg := Config{Hidden: 4, LR: 0.1, Epochs: 2, Batch: int(^uint(0) >> 1), Seed: 3}
	if _, err := Train(cfg, binarySyntheticData(4, 2)); err != nil {
		t.Fatalf("Train with oversized batch: %v", err)
	}
}

func TestTrainRowsLimitsTrainingToFixedPrefix(t *testing.T) {
	data := binarySyntheticData(8, 4)
	fullConfig := Config{Hidden: 4, LR: 0.1, Epochs: 3, Batch: 4, Seed: 17}
	full, err := Train(fullConfig, data)
	if err != nil {
		t.Fatalf("Train full data: %v", err)
	}
	prefixConfig := fullConfig
	prefixConfig.TrainRows = len(data.Train) / 2
	prefix, err := Train(prefixConfig, data)
	if err != nil {
		t.Fatalf("Train prefix: %v", err)
	}
	if prefix.ValLoss == full.ValLoss && prefix.ValAcc == full.ValAcc {
		t.Fatal("trainRows did not affect training")
	}

	prefixAgain, err := Train(prefixConfig, data)
	if err != nil {
		t.Fatalf("Train prefix again: %v", err)
	}
	if prefix.ValLoss != prefixAgain.ValLoss || prefix.ValAcc != prefixAgain.ValAcc {
		t.Fatalf("same trainRows prefix produced different metrics: %#v != %#v", prefix, prefixAgain)
	}
}

func TestTrainRejectsInvalidTrainRows(t *testing.T) {
	data := binarySyntheticData(4, 2)
	for _, rows := range []int{-1, len(data.Train) + 1} {
		cfg := Config{Hidden: 4, LR: 0.1, Epochs: 1, Batch: 2, TrainRows: rows}
		if _, err := Train(cfg, data); err == nil {
			t.Fatalf("Train accepted trainRows=%d", rows)
		}
	}
}

func TestEstimateCost(t *testing.T) {
	cost, err := EstimateCost(Config{Hidden: 128, Epochs: 5}, 50000)
	if err != nil {
		t.Fatalf("EstimateCost: %v", err)
	}
	if cost != 32000000 {
		t.Fatalf("EstimateCost = %g, want 32000000", cost)
	}
	for _, test := range []struct {
		cfg  Config
		rows int
	}{
		{cfg: Config{Hidden: 0, Epochs: 1}, rows: 10},
		{cfg: Config{Hidden: 4, Epochs: 0}, rows: 10},
		{cfg: Config{Hidden: 4, Epochs: 1}, rows: 0},
	} {
		if _, err := EstimateCost(test.cfg, test.rows); err == nil {
			t.Fatalf("EstimateCost accepted cfg=%#v rows=%d", test.cfg, test.rows)
		}
	}
}

func TestTrainRealMNISTSubset(t *testing.T) {
	dir := os.Getenv("MNIST_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "data")
	}
	if _, err := os.Stat(filepath.Join(dir, trainImagesFile)); os.IsNotExist(err) {
		t.Skip("MNIST dataset absent; set MNIST_DIR or place the verified files in data/")
	}

	data, err := LoadData(dir)
	if err != nil {
		t.Fatalf("LoadData(%q): %v", dir, err)
	}
	data.Train = data.Train[:128]
	data.Validation = data.Validation[:64]
	result, err := Train(Config{Hidden: 8, LR: 0.05, Epochs: 1, Batch: 32, Seed: 2026}, data)
	if err != nil {
		t.Fatalf("Train on real MNIST subset: %v", err)
	}
	if result.ValAcc < 0 || result.ValAcc > 1 || result.ValLoss < 0 {
		t.Fatalf("invalid real-MNIST metrics: %#v", result)
	}
}

func TestTrainRealMNISTCalibration(t *testing.T) {
	if os.Getenv("MNIST_CALIBRATION") != "1" {
		t.Skip("set MNIST_CALIBRATION=1 to run the full 5-epoch MNIST calibration")
	}
	dir := os.Getenv("MNIST_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "data")
	}
	if _, err := os.Stat(filepath.Join(dir, trainImagesFile)); os.IsNotExist(err) {
		t.Skip("MNIST dataset absent; set MNIST_DIR or place the verified files in data/")
	}

	data, err := LoadData(dir)
	if err != nil {
		t.Fatalf("LoadData(%q): %v", dir, err)
	}
	result, err := Train(Config{Hidden: 128, LR: 0.1, Epochs: 5, Batch: 64, Seed: 12345}, data)
	if err != nil {
		t.Fatalf("Train full MNIST calibration: %v", err)
	}
	t.Logf("full MNIST validation accuracy=%.6f loss=%.6f training_seconds=%.3f",
		result.ValAcc, result.ValLoss, result.TrainSec)
	if result.ValAcc < 0.95 || result.ValAcc > 1 || result.ValLoss < 0 {
		t.Fatalf("invalid full MNIST metrics: %#v", result)
	}
}

func TestTrainMNISTCalibrationSweep(t *testing.T) {
	if os.Getenv("MNIST_SWEEP") != "1" {
		t.Skip("set MNIST_SWEEP=1 to run the calibration sweep")
	}
	dir := os.Getenv("MNIST_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "data")
	}
	if _, err := os.Stat(filepath.Join(dir, trainImagesFile)); os.IsNotExist(err) {
		t.Skip("MNIST dataset absent; set MNIST_DIR or place the verified files in data/")
	}

	data, err := LoadData(dir)
	if err != nil {
		t.Fatalf("LoadData(%q): %v", dir, err)
	}
	points := []struct {
		hidden int
		epochs int
		rows   int
	}{
		{32, 2, 5000},
		{64, 3, 10000},
		{128, 2, 10000},
		{64, 5, 20000},
		{128, 3, 15000},
		{96, 5, 20000},
		{128, 5, 15000},
		{128, 4, 25000},
	}
	for _, point := range points {
		cfg := Config{
			Hidden:    point.hidden,
			LR:        0.1,
			Epochs:    point.epochs,
			Batch:     64,
			Seed:      12345,
			TrainRows: point.rows,
		}
		result, err := Train(cfg, data)
		if err != nil {
			t.Fatalf("Train hidden=%d epochs=%d rows=%d: %v", point.hidden, point.epochs, point.rows, err)
		}
		cost, err := EstimateCost(cfg, point.rows)
		if err != nil {
			t.Fatalf("EstimateCost hidden=%d epochs=%d rows=%d: %v", point.hidden, point.epochs, point.rows, err)
		}
		t.Logf("calibration hidden=%d epochs=%d rows=%d seconds=%.6f valAcc=%.6f cost=%g",
			point.hidden, point.epochs, point.rows, result.TrainSec, result.ValAcc, cost)
	}
}

func binarySyntheticData(perClass, validationPerClass int) *Data {
	data := &Data{}
	for class := 0; class < 2; class++ {
		for i := 0; i < perClass; i++ {
			pixels := []float64{0, 0}
			pixels[class] = 1
			data.Train = append(data.Train, Example{Pixels: pixels, Label: uint8(class)})
		}
		for i := 0; i < validationPerClass; i++ {
			pixels := []float64{0, 0}
			pixels[class] = 1
			data.Validation = append(data.Validation, Example{Pixels: pixels, Label: uint8(class)})
		}
	}
	return data
}
