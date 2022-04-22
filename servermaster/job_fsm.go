package servermaster

import (
	"sync"

	"github.com/hanfei1991/microcosm/lib"
	"github.com/hanfei1991/microcosm/pb"
	"github.com/hanfei1991/microcosm/pkg/errors"
	libModel "github.com/hanfei1991/microcosm/pkg/meta/orm/model"

	"github.com/pingcap/tiflow/dm/pkg/log"
	"go.uber.org/zap"
)

type jobHolder struct {
	lib.WorkerHandle
	*libModel.MasterMeta
	// True means the job is loaded from metastore during jobmanager failover.
	// Otherwise it is added by SubmitJob.
	addFromFailover bool
}

// JobFsm manages state of all job masters, job master state forms a finite-state
// machine. Note job master managed in JobFsm is in running status, which means
// the job is not terminated or finished.
//
// ,-------.                   ,-------.            ,-------.       ,--------.
// |WaitAck|                   |Online |            |Pending|       |Finished|
// `---+---'                   `---+---'            `---+---'       `---+----'
//     |                           |                    |               |
//     | Master                    |                    |               |
//     |  .OnWorkerOnline          |                    |               |
//     |-------------------------->|                    |               |
//     |                           |                    |               |
//     |                           | Master             |               |
//     |                           |   .OnWorkerOffline |               |
//     |                           |   (failover)       |               |
//     |                           |------------------->|               |
//     |                           |                    |               |
//     |                           | Master             |               |
//     |                           |   .OnWorkerOffline |               |
//     |                           |   (finish)         |               |
//     |                           |----------------------------------->|
//     |                           |                    |               |
//     | Master                    |                    |               |
//     |  .OnWorkerOffline         |                    |               |
//     |  (failover)               |                    |               |
//     |----------------------------------------------->|               |
//     |                           |                    |               |
//     | Master                    |                    |               |
//     |  .OnWorkerOffline         |                    |               |
//     |  (finish)                 |                    |               |
//     |--------------------------------------------------------------->|
//     |                           |                    |               |
//     |                           | Master             |               |
//     |                           |   .CreateWorker    |               |
//     |<-----------------------------------------------|               |
//     |                           |                    |               |
//     | Master                    |                    |               |
//     |  .OnWorkerDispatched      |                    |               |
//     |  (with error)             |                    |               |
//     |----------------------------------------------->|               |
//     |                           |                    |               |
//     |                           |                    |               |
//     |                           |                    |               |
type JobFsm struct {
	JobStats

	jobsMu      sync.RWMutex
	pendingJobs map[libModel.MasterID]*libModel.MasterMeta
	waitAckJobs map[libModel.MasterID]*jobHolder
	onlineJobs  map[libModel.MasterID]*jobHolder
}

// JobStats defines a statistics interface for JobFsm
type JobStats interface {
	JobCount(pb.QueryJobResponse_JobStatus) int
}

func NewJobFsm() *JobFsm {
	return &JobFsm{
		pendingJobs: make(map[libModel.MasterID]*libModel.MasterMeta),
		waitAckJobs: make(map[libModel.MasterID]*jobHolder),
		onlineJobs:  make(map[libModel.MasterID]*jobHolder),
	}
}

func (fsm *JobFsm) QueryOnlineJob(jobID libModel.MasterID) *jobHolder {
	fsm.jobsMu.RLock()
	defer fsm.jobsMu.RUnlock()
	return fsm.onlineJobs[jobID]
}

func (fsm *JobFsm) QueryJob(jobID libModel.MasterID) *pb.QueryJobResponse {
	checkPendingJob := func() *pb.QueryJobResponse {
		fsm.jobsMu.Lock()
		defer fsm.jobsMu.Unlock()

		meta, ok := fsm.pendingJobs[jobID]
		if !ok {
			return nil
		}
		resp := &pb.QueryJobResponse{
			Tp:     int64(meta.Tp),
			Config: meta.Config,
			Status: pb.QueryJobResponse_pending,
		}
		return resp
	}

	checkWaitAckJob := func() *pb.QueryJobResponse {
		fsm.jobsMu.Lock()
		defer fsm.jobsMu.Unlock()

		job, ok := fsm.waitAckJobs[jobID]
		if !ok {
			return nil
		}
		meta := job.MasterMeta
		resp := &pb.QueryJobResponse{
			Tp:     int64(meta.Tp),
			Config: meta.Config,
			Status: pb.QueryJobResponse_dispatched,
		}
		return resp
	}

	checkOnlineJob := func() *pb.QueryJobResponse {
		fsm.jobsMu.Lock()
		defer fsm.jobsMu.Unlock()

		job, ok := fsm.onlineJobs[jobID]
		if !ok {
			return nil
		}
		resp := &pb.QueryJobResponse{
			Tp:     int64(job.Tp),
			Config: job.Config,
			Status: pb.QueryJobResponse_online,
		}
		jobInfo, err := job.ToPB()
		// TODO (zixiong) ToPB should handle the tombstone situation gracefully.
		if err != nil {
			resp.Err = &pb.Error{
				Code:    pb.ErrorCode_UnknownError,
				Message: err.Error(),
			}
		} else if jobInfo != nil {
			resp.JobMasterInfo = jobInfo
		} else {
			// job master is just timeout but have not call OnOffline.
			return nil
		}
		return resp
	}

	if resp := checkPendingJob(); resp != nil {
		return resp
	}
	if resp := checkWaitAckJob(); resp != nil {
		return resp
	}
	return checkOnlineJob()
}

func (fsm *JobFsm) JobDispatched(job *libModel.MasterMeta, addFromFailover bool) {
	fsm.jobsMu.Lock()
	defer fsm.jobsMu.Unlock()
	fsm.waitAckJobs[job.ID] = &jobHolder{
		MasterMeta:      job,
		addFromFailover: addFromFailover,
	}
}

func (fsm *JobFsm) IterPendingJobs(dispatchJobFn func(job *libModel.MasterMeta) (string, error)) error {
	fsm.jobsMu.Lock()
	defer fsm.jobsMu.Unlock()

	for oldJobID, job := range fsm.pendingJobs {
		id, err := dispatchJobFn(job)
		if err != nil {
			return err
		}
		delete(fsm.pendingJobs, oldJobID)
		job.ID = id
		fsm.waitAckJobs[id] = &jobHolder{
			MasterMeta: job,
		}
		log.L().Info("job master recovered", zap.Any("job", job))
	}

	return nil
}

func (fsm *JobFsm) IterWaitAckJobs(dispatchJobFn func(job *libModel.MasterMeta) (string, error)) error {
	fsm.jobsMu.Lock()
	defer fsm.jobsMu.Unlock()

	for id, job := range fsm.waitAckJobs {
		if !job.addFromFailover {
			continue
		}
		_, err := dispatchJobFn(job.MasterMeta)
		if err != nil {
			return err
		}
		fsm.waitAckJobs[id].addFromFailover = false
		log.L().Info("tombstone job master doesn't receive heartbeat in time, recreate it", zap.Any("job", job))
	}

	return nil
}

func (fsm *JobFsm) JobOnline(worker lib.WorkerHandle) error {
	fsm.jobsMu.Lock()
	defer fsm.jobsMu.Unlock()

	job, ok := fsm.waitAckJobs[worker.ID()]
	if !ok {
		return errors.ErrWorkerNotFound.GenWithStackByArgs(worker.ID())
	}
	fsm.onlineJobs[worker.ID()] = &jobHolder{
		WorkerHandle: worker,
		MasterMeta:   job.MasterMeta,
	}
	delete(fsm.waitAckJobs, worker.ID())
	return nil
}

func (fsm *JobFsm) JobOffline(worker lib.WorkerHandle, needFailover bool) {
	fsm.jobsMu.Lock()
	defer fsm.jobsMu.Unlock()

	job, ok := fsm.onlineJobs[worker.ID()]
	if ok {
		delete(fsm.onlineJobs, worker.ID())
	} else {
		job, ok = fsm.waitAckJobs[worker.ID()]
		if !ok {
			log.L().Warn("unknown worker, ignore it", zap.String("id", worker.ID()))
			return
		}
		delete(fsm.waitAckJobs, worker.ID())
	}
	if needFailover {
		fsm.pendingJobs[worker.ID()] = job.MasterMeta
	}
}

func (fsm *JobFsm) JobDispatchFailed(worker lib.WorkerHandle) error {
	fsm.jobsMu.Lock()
	defer fsm.jobsMu.Unlock()

	job, ok := fsm.waitAckJobs[worker.ID()]
	if !ok {
		return errors.ErrWorkerNotFound.GenWithStackByArgs(worker.ID())
	}
	fsm.pendingJobs[worker.ID()] = job.MasterMeta
	delete(fsm.waitAckJobs, worker.ID())
	return nil
}

func (fsm *JobFsm) JobCount(status pb.QueryJobResponse_JobStatus) int {
	fsm.jobsMu.RLock()
	defer fsm.jobsMu.RUnlock()
	switch status {
	case pb.QueryJobResponse_pending:
		return len(fsm.pendingJobs)
	case pb.QueryJobResponse_dispatched:
		return len(fsm.waitAckJobs)
	case pb.QueryJobResponse_online:
		return len(fsm.onlineJobs)
	default:
		// TODO: support other job status count
		return 0
	}
}
