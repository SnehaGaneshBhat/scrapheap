package ml

import (
	"encoding/hex"
	"testing"
)

func TestValidationHashGolden(t *testing.T) {
	cfg := Config{Hidden: 8, LR: 0.1, Epochs: 12, Batch: 8, Seed: 12345}
	result, err := Train(cfg, determinismTestData())
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	const expected = "f03311bf72b3c7e4892fa536bd6c8a139077a9311a229edfc9da699cd0938b14"
	hash := ValidationHash(result)
	got := hex.EncodeToString(hash[:])
	if got != expected {
		t.Fatalf("ValidationHash = %s, want %s (valAcc=%.17g valLoss=%.17g)",
			got, expected, result.ValAcc, result.ValLoss)
	}
	result.TrainSec = 123.456
	hashWithDifferentDuration := ValidationHash(result)
	if hex.EncodeToString(hashWithDifferentDuration[:]) != expected {
		t.Fatal("ValidationHash unexpectedly includes nondeterministic TrainSec")
	}
}

func determinismTestData() *Data {
	data := &Data{}
	for class := 0; class < 2; class++ {
		for i := 0; i < 16; i++ {
			pixels := []float64{0, 0}
			pixels[class] = 1
			data.Train = append(data.Train, Example{Pixels: pixels, Label: uint8(class)})
		}
		for i := 0; i < 8; i++ {
			pixels := []float64{0, 0}
			pixels[class] = 1
			data.Validation = append(data.Validation, Example{Pixels: pixels, Label: uint8(class)})
		}
	}
	return data
}
