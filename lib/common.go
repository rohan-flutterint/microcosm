package lib

import (
	"fmt"
	"time"

	"github.com/hanfei1991/microcosm/model"
	"github.com/hanfei1991/microcosm/pkg/p2p"
)

type (
	MasterID         string
	WorkerID         string
	WorkerStatusCode int32
	WorkerType       int64

	Epoch         = int64
	monotonicTime = time.Duration
)

const (
	WorkerStatusNormal = WorkerStatusCode(iota + 1)
	WorkerStatusInit
	WorkerStatusError
)

const (
	// If no heartbeat response is received for workerTimeoutDuration,
	// a worker will commit suicide.
	workerTimeoutDuration = time.Second * 15

	// If no heartbeat is received for workerTimeoutDuration + workerTimeoutGracefulDuration,
	// the master will consider a worker dead.
	workerTimeoutGracefulDuration = time.Second * 5

	workerHeartbeatInterval = time.Second * 3

	workerReportStatusInterval = time.Second * 3

	masterHeartbeatCheckLoopInterval = time.Second * 1
)

type WorkerStatus struct {
	Code         WorkerStatusCode `json:"code"`
	ErrorMessage string           `json:"error-message"`
	Ext          interface{}      `json:"ext"`
}

func HeartbeatPingTopic(masterID MasterID) p2p.Topic {
	return fmt.Sprintf("heartbeat-ping-%s", string(masterID))
}

func HeartbeatPongTopic(masterID MasterID) p2p.Topic {
	return fmt.Sprintf("heartbeat-pong-%s", string(masterID))
}

func WorkloadReportTopic(masterID MasterID) p2p.Topic {
	return fmt.Sprintf("workload-report-%s", masterID)
}

func StatusUpdateTopic(masterID MasterID) p2p.Topic {
	return fmt.Sprintf("status-update-%s", masterID)
}

type HeartbeatPingMessage struct {
	SendTime     monotonicTime `json:"send-time"`
	FromWorkerID WorkerID      `json:"from-id"`
	Epoch        Epoch         `json:"epoch"`
}

type HeartbeatPongMessage struct {
	SendTime  monotonicTime `json:"send-time"`
	ReplyTime time.Time     `json:"reply-time"`
	Epoch     Epoch         `json:"epoch"`
}

type StatusUpdateMessage struct {
	WorkerID WorkerID     `json:"worker-id"`
	Status   WorkerStatus `json:"status"`
}

type WorkloadReportMessage struct {
	WorkerID WorkerID       `json:"worker-id"`
	Workload model.RescUnit `json:"workload"`
}

type (
	MasterMetaExt    = interface{}
	MasterMetaKVData struct {
		ID          MasterID   `json:"id"`
		Addr        string     `json:"addr"`
		NodeID      p2p.NodeID `json:"node-id"`
		Epoch       Epoch      `json:"epoch"`
		Initialized bool       `json:"initialized"`

		// Ext holds business-specific data
		Ext MasterMetaExt `json:"ext"`
	}
)

type MasterFailoverReasonCode int32

const (
	MasterTimedOut = MasterFailoverReasonCode(iota + 1)
	MasterReportedError
)

type MasterFailoverReason struct {
	Code         MasterFailoverReasonCode
	ErrorMessage string
}
