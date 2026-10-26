package ml

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDecodeExamplesSupportsRawAndGzipIDX(t *testing.T) {
	images := make([]byte, 16+2*expectedPixelLen)
	binary.BigEndian.PutUint32(images[0:4], imageMagic)
	binary.BigEndian.PutUint32(images[4:8], 2)
	binary.BigEndian.PutUint32(images[8:12], imageRows)
	binary.BigEndian.PutUint32(images[12:16], imageColumns)
	images[16] = 255
	images[16+expectedPixelLen+expectedPixelLen-1] = 128

	labels := make([]byte, 8+2)
	binary.BigEndian.PutUint32(labels[0:4], labelMagic)
	binary.BigEndian.PutUint32(labels[4:8], 2)
	labels[8] = 3
	labels[9] = 8

	compressedImages := gzipBytes(t, images)
	examples, err := decodeExamples(compressedImages, labels)
	if err != nil {
		t.Fatalf("decodeExamples: %v", err)
	}
	if len(examples) != 2 {
		t.Fatalf("got %d examples, want 2", len(examples))
	}
	if examples[0].Label != 3 || examples[1].Label != 8 {
		t.Fatalf("decoded labels = %d, %d; want 3, 8", examples[0].Label, examples[1].Label)
	}
	if examples[0].Pixels[0] != 1 || examples[1].Pixels[expectedPixelLen-1] != float64(128)/255 {
		t.Fatalf("pixel normalization incorrect")
	}
	if examples[0].Pixels[1] != 0 || examples[1].Pixels[0] != 0 {
		t.Fatalf("zero pixels were not normalized to zero")
	}
}

func TestDecodeExamplesRejectsInvalidIDX(t *testing.T) {
	validImages := make([]byte, 16+expectedPixelLen)
	binary.BigEndian.PutUint32(validImages[0:4], imageMagic)
	binary.BigEndian.PutUint32(validImages[4:8], 1)
	binary.BigEndian.PutUint32(validImages[8:12], imageRows)
	binary.BigEndian.PutUint32(validImages[12:16], imageColumns)
	validLabels := make([]byte, 9)
	binary.BigEndian.PutUint32(validLabels[0:4], labelMagic)
	binary.BigEndian.PutUint32(validLabels[4:8], 1)
	validLabels[8] = 10

	if _, err := decodeExamples(validImages, validLabels); err == nil {
		t.Fatal("decodeExamples accepted a digit label greater than 9")
	}

	validLabels[8] = 1
	if _, err := decodeExamples(validImages[:len(validImages)-1], validLabels); err == nil {
		t.Fatal("decodeExamples accepted a truncated image payload")
	}
}

func TestReadVerifiedFileRejectsWrongChecksum(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, trainImagesFile), []byte("not MNIST"), 0600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	if _, err := readVerifiedFile(dir, trainImagesFile); err == nil {
		t.Fatal("readVerifiedFile accepted a checksum mismatch")
	}
}

func TestSyntheticDataShape(t *testing.T) {
	data := syntheticData()
	if len(data.Train) != 2 || len(data.Validation) != 1 || len(data.Test) != 1 {
		t.Fatalf("synthetic split sizes = %d/%d/%d, want 2/1/1",
			len(data.Train), len(data.Validation), len(data.Test))
	}
	for _, examples := range [][]Example{data.Train, data.Validation, data.Test} {
		for _, example := range examples {
			if len(example.Pixels) != 2 || example.Label > 1 {
				t.Fatalf("invalid synthetic example: %#v", example)
			}
		}
	}
}

func TestLoadDataRealMNIST(t *testing.T) {
	dir := os.Getenv("MNIST_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "data")
	}
	present := 0
	for _, name := range []string{trainImagesFile, trainLabelsFile, testImagesFile, testLabelsFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			present++
		}
	}
	if present == 0 {
		t.Skip("MNIST dataset absent; set MNIST_DIR or place the four verified gzip files in data/")
	}

	started := time.Now()
	data, err := LoadData(dir)
	if err != nil {
		t.Fatalf("LoadData(%q): %v", dir, err)
	}
	t.Logf("LoadData duration: %.6f seconds", time.Since(started).Seconds())
	if len(data.Train) != trainSplitCount || len(data.Validation) != validationCount || len(data.Test) != testImageCount {
		t.Fatalf("dataset split sizes = %d/%d/%d, want %d/%d/%d",
			len(data.Train), len(data.Validation), len(data.Test),
			trainSplitCount, validationCount, testImageCount)
	}
	if len(data.Train[0].Pixels) != expectedPixelLen || data.Train[0].Label > 9 {
		t.Fatalf("first training example has invalid shape or label")
	}
	for i, pixel := range data.Train[0].Pixels {
		if pixel < 0 || pixel > 1 {
			t.Fatalf("normalized pixel %d = %g, outside [0, 1]", i, pixel)
		}
	}
}

func syntheticData() *Data {
	examples := make([]Example, 4)
	for i := range examples {
		pixels := make([]float64, 2)
		pixels[i%2] = 1
		examples[i] = Example{Pixels: pixels, Label: uint8(i % 2)}
	}
	return &Data{
		Train:      examples[:2],
		Validation: examples[2:3],
		Test:       examples[3:],
	}
}

func gzipBytes(t *testing.T, content []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(content); err != nil {
		t.Fatalf("write gzip fixture: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip fixture: %v", err)
	}
	return compressed.Bytes()
}
