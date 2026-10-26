package proto

import "encoding/json"

const (
	PullStatusTask = "task"
	PullStatusWait = "wait"
	PullStatusDone = "done"

	WorkerStatusActive   = "active"
	WorkerStatusLost     = "lost"
	WorkerStatusRejoined = "rejoined"

	JobStateWaiting = "waiting"
	JobStateRunning = "running"
	JobStateDone    = "done"

	TaskKindBurn  = "burn"
	TaskKindMnist = "mnist"

	ResultReasonOK        = "ok"
	ResultReasonDuplicate = "duplicate"
	ResultReasonStale     = "stale"
	ResultReasonUnknown   = "unknown"
)

type HelloRequest struct {
	Name    string `json:"name"`
	Cores   int    `json:"cores"`
	Threads int    `json:"threads"`
	Browser string `json:"browser"`
}

type DatasetFile struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type HelloResponse struct {
	WorkerID     string        `json:"workerId"`
	LeaseSec     int           `json:"leaseSec"`
	HeartbeatSec int           `json:"heartbeatSec"`
	JobKind      string        `json:"jobKind"`
	Rejoined     bool          `json:"rejoined"`
	Dataset      []DatasetFile `json:"dataset"`
}

type PullRequest struct {
	WorkerID string `json:"workerId"`
}

type TaskResponse struct {
	Status              string          `json:"status"`
	TaskID              string          `json:"taskId"`
	Kind                string          `json:"kind"`
	Payload             json.RawMessage `json:"payload"`
	LeaseDeadlineUnixMs int64           `json:"leaseDeadlineUnixMs"`
	Attempt             int             `json:"attempt"`
}

type WaitResponse struct {
	Status       string `json:"status"`
	RetryAfterMs int    `json:"retryAfterMs"`
}

type DoneResponse struct {
	Status string `json:"status"`
}

type ResultRequest struct {
	WorkerID string          `json:"workerId"`
	TaskID   string          `json:"taskId"`
	Attempt  int             `json:"attempt"`
	Result   json.RawMessage `json:"result"`
	Seconds  float64         `json:"seconds"`
}

type ResultResponse struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

type HeartbeatRequest struct {
	WorkerID string   `json:"workerId"`
	Running  []string `json:"running"`
}

type HeartbeatResponse struct {
	OK            bool `json:"ok"`
	UnknownWorker bool `json:"unknownWorker"`
}

type BurnPayload struct {
	Iters int `json:"iters"`
}

type BurnResult struct {
	Checksum string `json:"checksum"`
}

type WorkerInfo struct {
	ID        string  `json:"id"`
	Browser   string  `json:"browser"`
	Cores     int     `json:"cores"`
	Threads   int     `json:"threads"`
	TasksDone int     `json:"tasksDone"`
	Speed     float64 `json:"speed"`
	Status    string  `json:"status"`
}

type LeaderboardEntry struct {
	Config Config  `json:"config"`
	ValAcc float64 `json:"valAcc"`
}

type VerifyStats struct {
	Sampled    int `json:"sampled"`
	Matched    int `json:"matched"`
	Mismatched int `json:"mismatched"`
}

type BaselineInfo struct {
	Seconds float64 `json:"seconds"`
	Label   string  `json:"label"`
}

type BestResult struct {
	Config Config  `json:"config"`
	ValAcc float64 `json:"valAcc"`
}

type JobSnapshot struct {
	JobID       string             `json:"jobId"`
	State       string             `json:"state"`
	Total       int                `json:"total"`
	Done        int                `json:"done"`
	InFlight    int                `json:"inFlight"`
	Requeued    int                `json:"requeued"`
	ElapsedSec  float64            `json:"elapsedSec"`
	ETASec      float64            `json:"etaSec"`
	Workers     []WorkerInfo       `json:"workers"`
	Leaderboard []LeaderboardEntry `json:"leaderboard"`
	Verify      VerifyStats        `json:"verify"`
	Baseline    BaselineInfo       `json:"baseline"`
	Best        *BestResult        `json:"best"`
}

type APIResultResponse struct {
	Ready bool `json:"ready"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
