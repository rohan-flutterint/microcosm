package dm

import (
	"context"

	"github.com/pingcap/errors"
	"github.com/pingcap/tiflow/dm/dm/config"
	"github.com/pingcap/tiflow/dm/dm/unit"
	"github.com/pingcap/tiflow/dm/dumpling"
	"github.com/pingcap/tiflow/dm/pkg/log"

	"github.com/hanfei1991/microcosm/lib"
	"github.com/hanfei1991/microcosm/model"
)

var _ lib.WorkerImpl = &dumpWorker{}

type dumpWorker struct {
	*lib.DefaultBaseWorker

	cfg        *config.SubTaskConfig
	unitHolder *unitHolder
}

func (d *dumpWorker) InitImpl(ctx context.Context) error {
	d.unitHolder = newUnitHolder(dumpling.NewDumpling(d.cfg))
	return errors.Trace(d.unitHolder.init(ctx))
}

func (d *dumpWorker) Tick(ctx context.Context) error {
	d.unitHolder.lazyProcess()

	return nil
}

func (d *dumpWorker) Status() lib.WorkerStatus {
	hasResult, result := d.unitHolder.getResult()
	if !hasResult {
		return lib.WorkerStatus{Code: lib.WorkerStatusNormal}
	}
	if len(result.Errors) > 0 {
		return lib.WorkerStatus{
			Code:         lib.WorkerStatusError,
			ErrorMessage: unit.JoinProcessErrors(result.Errors),
		}
	}
	// should I keep the status unchanged for second call, or runtime will not
	// call Status again when it's the terminated status?
	return lib.WorkerStatus{Code: lib.WorkerStatusFinished}
}

func (d *dumpWorker) Workload() model.RescUnit {
	log.L().Info("dumpWorker.Workload")
	return 0
}

func (d *dumpWorker) OnMasterFailover(reason lib.MasterFailoverReason) error {
	log.L().Info("dumpWorker.OnMasterFailover")
	return nil
}

func (d *dumpWorker) CloseImpl(ctx context.Context) error {
	d.unitHolder.close()
	return nil
}