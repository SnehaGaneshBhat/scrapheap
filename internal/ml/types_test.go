package ml

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestConfigJSONRoundTrip(t *testing.T) {
	want := Config{Hidden: 128, LR: 0.1, Epochs: 5, Batch: 64, Seed: 12345}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal Config: %v", err)
	}

	const expected = `{"hidden":128,"lr":0.1,"epochs":5,"batch":64,"seed":12345}`
	if string(data) != expected {
		t.Fatalf("Config JSON = %s, want %s", data, expected)
	}

	var got Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal Config: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Config round trip = %#v, want %#v", got, want)
	}
}

func TestResultJSONRoundTrip(t *testing.T) {
	want := Result{ValAcc: 0.97, ValLoss: 0.12, TrainSec: 8.43}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal Result: %v", err)
	}

	const expected = `{"valAcc":0.97,"valLoss":0.12,"trainSec":8.43}`
	if string(data) != expected {
		t.Fatalf("Result JSON = %s, want %s", data, expected)
	}

	var got Result
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal Result: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Result round trip = %#v, want %#v", got, want)
	}
}

func TestTestResultJSONRoundTrip(t *testing.T) {
	want := TestResult{TestAcc: 0.98}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal TestResult: %v", err)
	}

	const expected = `{"testAcc":0.98}`
	if string(data) != expected {
		t.Fatalf("TestResult JSON = %s, want %s", data, expected)
	}

	var got TestResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal TestResult: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TestResult round trip = %#v, want %#v", got, want)
	}
}
