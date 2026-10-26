package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// TileStore holds accepted tile bytes for the current job.
type TileStore struct{ tiles map[uint32][]byte }

func NewTileStore() *TileStore { return &TileStore{tiles: make(map[uint32][]byte)} }

// Put copies rgba so the store does not retain a WebSocket read buffer.
func (s *TileStore) Put(taskID uint32, rgba []byte) {
	if s.tiles == nil {
		s.tiles = make(map[uint32][]byte)
	}
	s.tiles[taskID] = append([]byte(nil), rgba...)
}

// Get returns a copy of a stored tile.
func (s *TileStore) Get(taskID uint32) ([]byte, bool) {
	b, ok := s.tiles[taskID]
	return append([]byte(nil), b...), ok
}

func (s *TileStore) Count() int { return len(s.tiles) }
func (s *TileStore) Reset()     { s.tiles = make(map[uint32][]byte) }

// ImageHash hashes pixel bytes concatenated in ascending taskID order.
func (s *TileStore) ImageHash() string {
	ids := make([]uint32, 0, len(s.tiles))
	for id := range s.tiles {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	h := sha256.New()
	for _, id := range ids {
		_, _ = h.Write(s.tiles[id])
	}
	return hex.EncodeToString(h.Sum(nil))
}
