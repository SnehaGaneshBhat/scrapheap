package hub

import (
	"testing"

	"github.com/scrap-heap/backend/internal/proto"
)

func TestBuildTilesUsesRowMajorTaskIDs(t *testing.T) {
	tests := []struct {
		name string
		job  JobConfig
	}{
		{name: "default canvas", job: DefaultJobConfig()},
		{name: "128 pixel canvas", job: JobConfig{X0: -2.5, Y0: -1.75, Scale: .01, MaxIter: 100, CanvasSize: 128}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tiles := buildTiles(1, tt.job, nil)
			cols := (tt.job.CanvasSize + proto.TileSize - 1) / proto.TileSize
			rows := cols
			if len(tiles) != rows*cols {
				t.Fatalf("got %d tiles, want %d", len(tiles), rows*cols)
			}
			for row := 0; row < rows; row++ {
				for col := 0; col < cols; col++ {
					id := row*cols + col
					task := tiles[id].Task
					if task.TaskID != uint32(id) || int(task.X) != col*proto.TileSize || int(task.Y) != row*proto.TileSize {
						t.Fatalf("grid (%d,%d): taskId=%d x=%d y=%d", col, row, task.TaskID, task.X, task.Y)
					}
				}
			}
		})
	}
}
