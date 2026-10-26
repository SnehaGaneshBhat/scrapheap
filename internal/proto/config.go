package proto

// Config is an MNIST task configuration.
type Config struct {
	Hidden int     `json:"hidden"`
	LR     float64 `json:"lr"`
	Epochs int     `json:"epochs"`
	Batch  int     `json:"batch"`
	Seed   int     `json:"seed"`
}

// MnistResult contains the metrics returned by an MNIST task.
type MnistResult struct {
	ValAcc   float64 `json:"valAcc"`
	ValLoss  float64 `json:"valLoss"`
	TrainSec float64 `json:"trainSec"`
}
