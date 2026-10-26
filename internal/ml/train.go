package ml

import (
	"errors"
	"fmt"
	"math"
	"time"
)

const outputSize = 10

type model struct {
	inputSize  int
	hiddenSize int
	weights1   []float64
	biases1    []float64
	weights2   []float64
	biases2    []float64
}

// Train trains a single-threaded model and reports validation metrics only.
func Train(cfg Config, d *Data) (Result, error) {
	started := time.Now()
	if err := validateTrainingInput(cfg, d); err != nil {
		return Result{}, &Error{Op: "Train", Err: err}
	}

	network, err := trainModel(cfg, d)
	if err != nil {
		return Result{}, &Error{Op: "Train", Err: err}
	}
	accuracy, loss, err := evaluate(network, d.Validation)
	if err != nil {
		return Result{}, &Error{Op: "Train", Err: fmt.Errorf("validation: %w", err)}
	}
	return Result{
		ValAcc:   accuracy,
		ValLoss:  loss,
		TrainSec: time.Since(started).Seconds(),
	}, nil
}

func trainModel(cfg Config, d *Data) (*model, error) {
	trainingRows := len(d.Train)
	if cfg.TrainRows > 0 {
		trainingRows = cfg.TrainRows
	}
	trainExamples := d.Train[:trainingRows]
	inputSize := len(trainExamples[0].Pixels)
	network := newModel(cfg, inputSize)
	order := make([]int, len(trainExamples))
	for i := range order {
		order[i] = i
	}
	shuffleRNG := newPRNG(deriveSeed(cfg.Seed, shuffleStream))

	for epoch := 0; epoch < cfg.Epochs; epoch++ {
		shuffle(order, shuffleRNG)
		for batchStart := 0; batchStart < len(order); {
			batchSize := cfg.Batch
			if remaining := len(order) - batchStart; batchSize > remaining {
				batchSize = remaining
			}
			batchEnd := batchStart + batchSize
			if err := trainBatch(network, trainExamples, order[batchStart:batchEnd], cfg.LR); err != nil {
				return nil, err
			}
			batchStart = batchEnd
		}
	}
	return network, nil
}

// EvalTest retrains cfg and evaluates it against the held-back test set.
func EvalTest(cfg Config, d *Data) (TestResult, error) {
	if err := validateTrainingInput(cfg, d); err != nil {
		return TestResult{}, &Error{Op: "EvalTest", Err: err}
	}
	if len(d.Test) == 0 {
		return TestResult{}, &Error{Op: "EvalTest", Err: errors.New("test set must not be empty")}
	}
	if err := validateExamples(d.Test, len(d.Train[0].Pixels), "test"); err != nil {
		return TestResult{}, &Error{Op: "EvalTest", Err: err}
	}
	network, err := trainModel(cfg, d)
	if err != nil {
		return TestResult{}, &Error{Op: "EvalTest", Err: err}
	}
	accuracy, _, err := evaluate(network, d.Test)
	if err != nil {
		return TestResult{}, &Error{Op: "EvalTest", Err: fmt.Errorf("test: %w", err)}
	}
	return TestResult{TestAcc: accuracy}, nil
}

func validateTrainingInput(cfg Config, d *Data) error {
	if cfg.Hidden <= 0 {
		return errors.New("hidden must be positive")
	}
	if cfg.LR <= 0 || math.IsNaN(cfg.LR) || math.IsInf(cfg.LR, 0) {
		return errors.New("learning rate must be finite and positive")
	}
	if cfg.Epochs <= 0 {
		return errors.New("epochs must be positive")
	}
	if cfg.Batch <= 0 {
		return errors.New("batch size must be positive")
	}
	if d == nil {
		return errors.New("data must not be nil")
	}
	if len(d.Train) == 0 {
		return errors.New("training set must not be empty")
	}
	if cfg.TrainRows < 0 {
		return errors.New("trainRows must not be negative")
	}
	if cfg.TrainRows > len(d.Train) {
		return fmt.Errorf("trainRows %d exceeds training set size %d", cfg.TrainRows, len(d.Train))
	}
	if len(d.Validation) == 0 {
		return errors.New("validation set must not be empty")
	}

	inputSize := len(d.Train[0].Pixels)
	if inputSize == 0 {
		return errors.New("examples must have at least one pixel")
	}
	maxInt := int(^uint(0) >> 1)
	if cfg.Hidden > maxInt/outputSize || inputSize > maxInt/cfg.Hidden {
		return errors.New("model dimensions overflow")
	}
	if err := validateExamples(d.Train, inputSize, "training"); err != nil {
		return err
	}
	if err := validateExamples(d.Validation, inputSize, "validation"); err != nil {
		return err
	}
	return nil
}

// EstimateCost returns the planned hidden-size × epochs × rows work proxy.
func EstimateCost(cfg Config, rows int) (float64, error) {
	if cfg.Hidden <= 0 || cfg.Epochs <= 0 || rows <= 0 {
		return 0, errors.New("hidden size, epochs, and rows must be positive")
	}
	return float64(cfg.Hidden) * float64(cfg.Epochs) * float64(rows), nil
}

func validateExamples(examples []Example, inputSize int, setName string) error {
	for i, example := range examples {
		if len(example.Pixels) != inputSize {
			return fmt.Errorf("%s example %d has %d pixels, want %d", setName, i, len(example.Pixels), inputSize)
		}
		if example.Label >= outputSize {
			return fmt.Errorf("%s example %d has invalid label %d", setName, i, example.Label)
		}
		for pixelIndex, pixel := range example.Pixels {
			if math.IsNaN(pixel) || math.IsInf(pixel, 0) {
				return fmt.Errorf("%s example %d pixel %d is not finite", setName, i, pixelIndex)
			}
		}
	}
	return nil
}

func newModel(cfg Config, inputSize int) *model {
	network := &model{
		inputSize:  inputSize,
		hiddenSize: cfg.Hidden,
		weights1:   make([]float64, inputSize*cfg.Hidden),
		biases1:    make([]float64, cfg.Hidden),
		weights2:   make([]float64, cfg.Hidden*outputSize),
		biases2:    make([]float64, outputSize),
	}

	rng := newPRNG(deriveSeed(cfg.Seed, initStream))
	limit1 := math.Sqrt(6 / float64(inputSize))
	for i := range network.weights1 {
		network.weights1[i] = float64((rng.nextFloat64()*2 - 1) * limit1)
	}
	limit2 := math.Sqrt(6 / float64(cfg.Hidden))
	for i := range network.weights2 {
		network.weights2[i] = float64((rng.nextFloat64()*2 - 1) * limit2)
	}
	return network
}

func (m *model) logits(pixels []float64) []float64 {
	hidden := m.hiddenActivations(pixels)
	logits := make([]float64, outputSize)
	for output := 0; output < outputSize; output++ {
		sum := m.biases2[output]
		weightOffset := output * m.hiddenSize
		for hiddenIndex, activation := range hidden {
			sum = sum + float64(m.weights2[weightOffset+hiddenIndex]*activation)
		}
		logits[output] = sum
	}
	return logits
}

func (m *model) hiddenActivations(pixels []float64) []float64 {
	hidden := make([]float64, m.hiddenSize)
	for hiddenIndex := 0; hiddenIndex < m.hiddenSize; hiddenIndex++ {
		sum := m.biases1[hiddenIndex]
		weightOffset := hiddenIndex * m.inputSize
		for pixelIndex, pixel := range pixels {
			sum = sum + float64(m.weights1[weightOffset+pixelIndex]*pixel)
		}
		if sum > 0 {
			hidden[hiddenIndex] = sum
		}
	}
	return hidden
}

func trainBatch(m *model, examples []Example, order []int, learningRate float64) error {
	gradWeights1 := make([]float64, len(m.weights1))
	gradBiases1 := make([]float64, len(m.biases1))
	gradWeights2 := make([]float64, len(m.weights2))
	gradBiases2 := make([]float64, len(m.biases2))
	hidden := make([]float64, m.hiddenSize)

	for _, exampleIndex := range order {
		example := examples[exampleIndex]
		for hiddenIndex := 0; hiddenIndex < m.hiddenSize; hiddenIndex++ {
			sum := m.biases1[hiddenIndex]
			weightOffset := hiddenIndex * m.inputSize
			for pixelIndex, pixel := range example.Pixels {
				sum = sum + float64(m.weights1[weightOffset+pixelIndex]*pixel)
			}
			if sum > 0 {
				hidden[hiddenIndex] = sum
			} else {
				hidden[hiddenIndex] = 0
			}
		}

		logits := make([]float64, outputSize)
		for output := 0; output < outputSize; output++ {
			sum := m.biases2[output]
			weightOffset := output * m.hiddenSize
			for hiddenIndex, activation := range hidden {
				sum = sum + float64(m.weights2[weightOffset+hiddenIndex]*activation)
			}
			logits[output] = sum
		}
		probabilities, err := softmax(logits)
		if err != nil {
			return fmt.Errorf("example %d: %w", exampleIndex, err)
		}
		probabilities[example.Label] -= 1

		for output := 0; output < outputSize; output++ {
			delta := probabilities[output]
			gradBiases2[output] = gradBiases2[output] + delta
			weightOffset := output * m.hiddenSize
			for hiddenIndex, activation := range hidden {
				gradWeights2[weightOffset+hiddenIndex] =
					gradWeights2[weightOffset+hiddenIndex] + float64(delta*activation)
			}
		}

		for hiddenIndex := 0; hiddenIndex < m.hiddenSize; hiddenIndex++ {
			delta := 0.0
			for output := 0; output < outputSize; output++ {
				delta = delta + float64(m.weights2[output*m.hiddenSize+hiddenIndex]*probabilities[output])
			}
			if hidden[hiddenIndex] == 0 {
				delta = 0
			}
			gradBiases1[hiddenIndex] = gradBiases1[hiddenIndex] + delta
			weightOffset := hiddenIndex * m.inputSize
			for pixelIndex, pixel := range example.Pixels {
				gradWeights1[weightOffset+pixelIndex] =
					gradWeights1[weightOffset+pixelIndex] + float64(delta*pixel)
			}
		}
	}

	scale := 1 / float64(len(order))
	for i := range m.weights1 {
		m.weights1[i] = m.weights1[i] - float64(learningRate*float64(gradWeights1[i]*scale))
	}
	for i := range m.biases1 {
		m.biases1[i] = m.biases1[i] - float64(learningRate*float64(gradBiases1[i]*scale))
	}
	for i := range m.weights2 {
		m.weights2[i] = m.weights2[i] - float64(learningRate*float64(gradWeights2[i]*scale))
	}
	for i := range m.biases2 {
		m.biases2[i] = m.biases2[i] - float64(learningRate*float64(gradBiases2[i]*scale))
	}
	return nil
}

func evaluate(m *model, examples []Example) (float64, float64, error) {
	correct := 0
	totalLoss := 0.0
	for i, example := range examples {
		logits := m.logits(example.Pixels)
		maximum := logits[0]
		predicted := 0
		for output := 1; output < outputSize; output++ {
			if logits[output] > maximum {
				maximum = logits[output]
				predicted = output
			}
		}

		sum := 0.0
		for _, logit := range logits {
			sum = sum + detExp(logit-maximum)
		}
		loss := maximum + detLog(sum) - logits[example.Label]
		if math.IsNaN(loss) || math.IsInf(loss, 0) {
			return 0, 0, fmt.Errorf("example %d has non-finite cross-entropy", i)
		}
		totalLoss = totalLoss + loss
		if predicted == int(example.Label) {
			correct++
		}
	}
	count := float64(len(examples))
	return float64(correct) / count, totalLoss / count, nil
}

func shuffle(order []int, rng *prng) {
	for i := len(order) - 1; i > 0; i-- {
		bound := uint64(i + 1)
		threshold := -bound % bound
		value := rng.nextUint64()
		for value < threshold {
			value = rng.nextUint64()
		}
		j := int(value % bound)
		order[i], order[j] = order[j], order[i]
	}
}
