package fake

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/hanfei1991/microcosm/lib"
	"github.com/hanfei1991/microcosm/model"
	dcontext "github.com/hanfei1991/microcosm/pkg/context"
	"github.com/pingcap/tiflow/dm/pkg/log"
	"go.uber.org/zap"
)

var _ lib.Worker = (*dummyWorker)(nil)

type (
	Worker      = dummyWorker
	dummyWorker struct {
		*lib.defaultBaseWorker

		init   bool
		closed int32
		tick   int64
	}
)

func (d *dummyWorker) InitImpl(ctx context.Context) error {
	if !d.init {
		d.init = true
		return nil
	}
	return errors.New("repeated init")
}

func (d *dummyWorker) Tick(ctx context.Context) error {
	if !d.init {
		return errors.New("not yet init")
	}

	log.L().Info("FakeWorker: Tick", zap.String("worker-id", d.ID()))
	if atomic.LoadInt32(&d.closed) == 1 {
		return nil
	}
	d.tick++
	return nil
}

func (d *dummyWorker) Status() lib.WorkerStatus {
	if d.init {
		return lib.WorkerStatus{Code: lib.WorkerStatusNormal, Ext: d.tick}
	}
	return lib.WorkerStatus{Code: lib.WorkerStatusCreated}
}

func (d *dummyWorker) Workload() model.RescUnit {
	return model.RescUnit(10)
}

func (d *dummyWorker) OnMasterFailover(_ lib.MasterFailoverReason) error {
	return nil
}

func (d *dummyWorker) CloseImpl(ctx context.Context) error {
	atomic.StoreInt32(&d.closed, 1)
	return nil
}

func NewDummyWorker(ctx *dcontext.Context, id lib.WorkerID, masterID lib.MasterID, _ lib.WorkerConfig) lib.Worker {
	ret := &dummyWorker{}
	dependencies := ctx.Dependencies
	ret.defaultBaseWorker = lib.NewBaseWorker(
		ret,
		dependencies.MessageHandlerManager,
		dependencies.MessageRouter,
		dependencies.MetaKVClient,
		id,
		masterID)

	return ret
}
