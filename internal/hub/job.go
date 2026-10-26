package hub

import (
	"math"

	"github.com/scrap-heap/backend/internal/proto"
	"github.com/scrap-heap/backend/internal/sched"
)

// JobConfig defines a square canvas and the Mandelbrot view. Defaults are
// placeholders for the full set until the team freezes a shared zoom.
type JobConfig struct {
	X0, Y0, Scale float64
	MaxIter       uint32
	CanvasSize    int
}

func DefaultJobConfig() JobConfig {
	return JobConfig{X0: -2.5, Y0: -1.75, Scale: 3.5 / proto.CanvasSize, MaxIter: 1000, CanvasSize: proto.CanvasSize}
}

// CostEstimator returns work in benchmark-tile equivalents.
type CostEstimator interface{ EstimateCost(proto.Task) float64 }

// UnitCost assigns equal estimated cost to every tile.
type UnitCost struct{}

func (UnitCost) EstimateCost(proto.Task) float64 { return 1 }

func buildTiles(jobID uint32, cfg JobConfig, estimator CostEstimator) []sched.Tile {
	if cfg.CanvasSize <= 0 || cfg.Scale <= 0 || math.IsNaN(cfg.Scale) || math.IsInf(cfg.Scale, 0) || cfg.MaxIter == 0 {
		return nil
	}
	if estimator == nil {
		estimator = UnitCost{}
	}
	count := (cfg.CanvasSize + proto.TileSize - 1) / proto.TileSize
	tiles := make([]sched.Tile, 0, count*count)
	for ty := 0; ty < count; ty++ {
		for tx := 0; tx < count; tx++ {
			x, y := tx*proto.TileSize, ty*proto.TileSize
			w, h := min(proto.TileSize, cfg.CanvasSize-x), min(proto.TileSize, cfg.CanvasSize-y)
			task := proto.Task{JobID: jobID, TaskID: uint32(ty*count + tx), X: uint16(x), Y: uint16(y), W: uint16(w), H: uint16(h), X0: cfg.X0 + float64(x)*cfg.Scale, Y0: cfg.Y0 + float64(y)*cfg.Scale, Scale: cfg.Scale, MaxIter: cfg.MaxIter}
			tiles = append(tiles, sched.Tile{Task: task, EstimatedCost: estimator.EstimateCost(task)})
		}
	}
	return tiles
}
