//go:build js && wasm

package main

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"syscall/js"

	"scrapheap/internal/ml"
)

const (
	trainRows      = 50000
	validationRows = 10000
	imageSize      = 28 * 28
)

var (
	dataset *ml.Data
	loadFn  js.Func
	trainFn js.Func
)

func main() {
	loadFn = js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 2 {
			return errorJSON(errors.New("scrapHeapLoadDataset expects the training images and labels"))
		}
		files := make([][]byte, len(args))
		for i, value := range args {
			files[i] = make([]byte, value.Get("byteLength").Int())
			if js.CopyBytesToGo(files[i], value) != len(files[i]) {
				return errorJSON(fmt.Errorf("could not copy MNIST file %d", i))
			}
		}
		loaded, err := decodeData(files)
		if err != nil {
			return errorJSON(err)
		}
		dataset = loaded
		return `{"ready":true}`
	})
	trainFn = js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 {
			return errorJSON(errors.New("scrapHeapTrain expects a JSON config"))
		}
		if dataset == nil {
			return errorJSON(errors.New("MNIST dataset has not been loaded"))
		}
		var cfg ml.Config
		if err := json.Unmarshal([]byte(args[0].String()), &cfg); err != nil {
			return errorJSON(fmt.Errorf("decode config: %w", err))
		}
		result, err := ml.Train(cfg, dataset)
		if err != nil {
			return errorJSON(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return errorJSON(err)
		}
		return string(encoded)
	})
	js.Global().Set("scrapHeapLoadDataset", loadFn)
	js.Global().Set("scrapHeapTrain", trainFn)
	select {}
}

func errorJSON(err error) string {
	encoded, _ := json.Marshal(map[string]string{"error": err.Error()})
	return string(encoded)
}

func decodeData(files [][]byte) (*ml.Data, error) {
	if len(files) != 2 {
		return nil, errors.New("expected the two MNIST training files")
	}
	trainImages, err := unpackIDX(files[0])
	if err != nil {
		return nil, fmt.Errorf("training images: %w", err)
	}
	trainLabels, err := unpackIDX(files[1])
	if err != nil {
		return nil, fmt.Errorf("training labels: %w", err)
	}
	training, err := examples(trainImages, trainLabels, 60000)
	if err != nil {
		return nil, fmt.Errorf("training set: %w", err)
	}
	return &ml.Data{Train: training[:trainRows], Validation: training[trainRows : trainRows+validationRows]}, nil
}

func unpackIDX(input []byte) ([]byte, error) {
	if len(input) < 2 || input[0] != 0x1f || input[1] != 0x8b {
		return input, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(input))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func examples(imageBytes, labelBytes []byte, want int) ([]ml.Example, error) {
	if len(imageBytes) < 16 || len(labelBytes) < 8 {
		return nil, errors.New("IDX file header is truncated")
	}
	if binary.BigEndian.Uint32(imageBytes[:4]) != 2051 || binary.BigEndian.Uint32(labelBytes[:4]) != 2049 {
		return nil, errors.New("invalid IDX magic number")
	}
	count := int(binary.BigEndian.Uint32(imageBytes[4:8]))
	labelCount := int(binary.BigEndian.Uint32(labelBytes[4:8]))
	if count != want || labelCount != want {
		return nil, fmt.Errorf("IDX row count mismatch: got images=%d labels=%d, want %d", count, labelCount, want)
	}
	if binary.BigEndian.Uint32(imageBytes[8:12]) != 28 || binary.BigEndian.Uint32(imageBytes[12:16]) != 28 {
		return nil, errors.New("MNIST images must be 28 by 28 pixels")
	}
	if len(imageBytes) != 16+count*imageSize || len(labelBytes) != 8+count {
		return nil, errors.New("IDX content length does not match header")
	}
	result := make([]ml.Example, count)
	for row := range result {
		label := labelBytes[8+row]
		if label > 9 {
			return nil, fmt.Errorf("label %d is outside 0..9", label)
		}
		pixels := make([]float64, imageSize)
		start := 16 + row*imageSize
		for i, pixel := range imageBytes[start : start+imageSize] {
			pixels[i] = float64(pixel) / 255
		}
		result[row] = ml.Example{Pixels: pixels, Label: label}
	}
	return result, nil
}
