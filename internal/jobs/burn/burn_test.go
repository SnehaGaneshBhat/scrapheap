package burn

import (
	"encoding/json"
	"strconv"
	"testing"

	"scrapheap/internal/proto"
)

func TestBurnSourceAndReducer(t *testing.T) {
	const iters = 25
	if Burn(iters) != Burn(iters) {
		t.Fatal("Burn is not deterministic")
	}
	source := NewSource(2, iters)
	tasks := source.Tasks()
	if len(tasks) != 2 || tasks[0].ID != "burn-0" || tasks[1].ID != "burn-1" || tasks[0].Kind != proto.TaskKindBurn {
		t.Fatalf("unexpected task list: %+v", tasks)
	}
	var payload proto.BurnPayload
	if err := json.Unmarshal(tasks[0].Payload, &payload); err != nil || payload.Iters != iters {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}

	reducer := NewReducer(iters)
	good, _ := json.Marshal(proto.BurnResult{Checksum: strconv.FormatUint(Burn(iters), 10)})
	reducer.Accept(tasks[0], good, "worker-1")
	reducer.Accept(tasks[1], []byte(`{"checksum":"0"}`), "worker-2")
	accepted, wrong := reducer.Stats()
	if accepted != 2 || wrong != 1 {
		t.Fatalf("stats=(%d,%d), want (2,1)", accepted, wrong)
	}
	if got := reducer.WrongTasks(); len(got) != 1 || got[0] != "burn-1" {
		t.Fatalf("wrong task list=%v", got)
	}
}
