package ml

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	trainImagesFile = "train-images-idx3-ubyte.gz"
	trainLabelsFile = "train-labels-idx1-ubyte.gz"
	testImagesFile  = "t10k-images-idx3-ubyte.gz"
	testLabelsFile  = "t10k-labels-idx1-ubyte.gz"

	imageMagic       = uint32(2051)
	labelMagic       = uint32(2049)
	imageRows        = 28
	imageColumns     = 28
	trainImageCount  = 60000
	trainSplitCount  = 50000
	validationCount  = trainImageCount - trainSplitCount
	testImageCount   = 10000
	expectedPixelLen = imageRows * imageColumns
)

var datasetSHA256 = map[string]string{
	trainImagesFile: "440fcabf73cc546fa21475e81ea370265605f56be210a4024d2ca8f203523609",
	trainLabelsFile: "3552534a0a558bbed6aed32b30c495cca23d567ec52cac8be1a0730e8010255c",
	testImagesFile:  "8d422c7b0a1c1c79245a5bcf07fe86e33eeafee792b84584aec276f5a2dbc4e6",
	testLabelsFile:  "f7ae60f92e00ec6debd23a6088c31dbd2371eca3ffa0defaefb259924204aec6",
}

// LoadData loads and verifies the MNIST data files from dir.
func LoadData(dir string) (*Data, error) {
	trainImages, err := readVerifiedFile(dir, trainImagesFile)
	if err != nil {
		return nil, &Error{Op: "LoadData", Err: err}
	}
	trainLabels, err := readVerifiedFile(dir, trainLabelsFile)
	if err != nil {
		return nil, &Error{Op: "LoadData", Err: err}
	}
	testImages, err := readVerifiedFile(dir, testImagesFile)
	if err != nil {
		return nil, &Error{Op: "LoadData", Err: err}
	}
	testLabels, err := readVerifiedFile(dir, testLabelsFile)
	if err != nil {
		return nil, &Error{Op: "LoadData", Err: err}
	}

	train, err := decodeExamples(trainImages, trainLabels)
	if err != nil {
		return nil, &Error{Op: "LoadData", Err: fmt.Errorf("training set: %w", err)}
	}
	test, err := decodeExamples(testImages, testLabels)
	if err != nil {
		return nil, &Error{Op: "LoadData", Err: fmt.Errorf("test set: %w", err)}
	}
	if len(train) != trainImageCount {
		return nil, &Error{Op: "LoadData", Err: fmt.Errorf("training set has %d examples, want %d", len(train), trainImageCount)}
	}
	if len(test) != testImageCount {
		return nil, &Error{Op: "LoadData", Err: fmt.Errorf("test set has %d examples, want %d", len(test), testImageCount)}
	}

	return &Data{
		Train:      train[:trainSplitCount],
		Validation: train[trainSplitCount:],
		Test:       test,
	}, nil
}

func readVerifiedFile(dir, name string) ([]byte, error) {
	path := filepath.Join(dir, name)
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	sum := sha256.Sum256(content)
	actual := hex.EncodeToString(sum[:])
	expected, ok := datasetSHA256[name]
	if !ok {
		return nil, fmt.Errorf("no SHA-256 recorded for %s", name)
	}
	if actual != expected {
		return nil, fmt.Errorf("SHA-256 mismatch for %s: got %s, want %s", name, actual, expected)
	}
	return content, nil
}

func decodeExamples(imageFile, labelFile []byte) ([]Example, error) {
	imageData, err := decodeIDX(imageFile)
	if err != nil {
		return nil, fmt.Errorf("decode images: %w", err)
	}
	labelData, err := decodeIDX(labelFile)
	if err != nil {
		return nil, fmt.Errorf("decode labels: %w", err)
	}

	if len(imageData) < 16 {
		return nil, errors.New("image IDX header is truncated")
	}
	if len(labelData) < 8 {
		return nil, errors.New("label IDX header is truncated")
	}
	if got := binary.BigEndian.Uint32(imageData[:4]); got != imageMagic {
		return nil, fmt.Errorf("invalid image IDX magic %d, want %d", got, imageMagic)
	}
	if got := binary.BigEndian.Uint32(labelData[:4]); got != labelMagic {
		return nil, fmt.Errorf("invalid label IDX magic %d, want %d", got, labelMagic)
	}

	imageCount := binary.BigEndian.Uint32(imageData[4:8])
	rows := binary.BigEndian.Uint32(imageData[8:12])
	columns := binary.BigEndian.Uint32(imageData[12:16])
	labelCount := binary.BigEndian.Uint32(labelData[4:8])
	if rows != imageRows || columns != imageColumns {
		return nil, fmt.Errorf("unexpected image dimensions %dx%d, want %dx%d", rows, columns, imageRows, imageColumns)
	}
	if imageCount != labelCount {
		return nil, fmt.Errorf("image count %d does not match label count %d", imageCount, labelCount)
	}
	if uint64(len(imageData)-16) != uint64(imageCount)*expectedPixelLen {
		return nil, fmt.Errorf("image payload length does not match count %d and dimensions %dx%d", imageCount, rows, columns)
	}
	if uint64(len(labelData)-8) != uint64(labelCount) {
		return nil, fmt.Errorf("label payload length does not match count %d", labelCount)
	}

	examples := make([]Example, int(imageCount))
	pixels := imageData[16:]
	labels := labelData[8:]
	for i := range examples {
		if labels[i] > 9 {
			return nil, fmt.Errorf("invalid digit label %d at index %d", labels[i], i)
		}
		start := i * expectedPixelLen
		normalized := make([]float64, expectedPixelLen)
		for j, pixel := range pixels[start : start+expectedPixelLen] {
			normalized[j] = float64(pixel) / 255
		}
		examples[i] = Example{Pixels: normalized, Label: labels[i]}
	}
	return examples, nil
}

func decodeIDX(content []byte) ([]byte, error) {
	if len(content) < 2 || content[0] != 0x1f || content[1] != 0x8b {
		return content, nil
	}

	reader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("open gzip stream: %w", err)
	}
	decoded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read gzip stream: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close gzip stream: %w", closeErr)
	}
	return decoded, nil
}
