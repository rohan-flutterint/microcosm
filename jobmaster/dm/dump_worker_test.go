package dm

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pingcap/tidb-tools/pkg/filter"
	"github.com/pingcap/tiflow/dm/dm/config"
	"github.com/pingcap/tiflow/dm/pkg/log"
	"github.com/pingcap/tiflow/dm/pkg/utils"
	"github.com/stretchr/testify/require"

	"github.com/hanfei1991/microcosm/lib"
	"github.com/hanfei1991/microcosm/pkg/adapter"
	"github.com/hanfei1991/microcosm/pkg/metadata"
	"github.com/hanfei1991/microcosm/pkg/p2p"
)

func mockWorker(workerID lib.WorkerID, masterID lib.MasterID) *dumpWorker {
	ret := &dumpWorker{}
	base := lib.NewBaseWorker(
		ret,
		p2p.NewMockMessageHandlerManager(),
		p2p.NewMockMessageSender(),
		metadata.NewMetaMock(),
		workerID,
		masterID)
	ret.BaseWorker = base
	cfg := &config.SubTaskConfig{
		From: config.DBConfig{
			Host:     "127.0.0.1",
			Port:     3306,
			User:     "root",
			Password: "123456",
		},
		To: config.DBConfig{
			Host:     "127.0.0.1",
			Port:     4000,
			User:     "root",
			Password: "",
		},
		ServerID:   102,
		MetaSchema: "db_test",
		Name:       "db_ut",
		Mode:       config.ModeAll,
		Flavor:     "mysql",
		LoaderConfig: config.LoaderConfig{
			Dir: "/tmp/dftest",
		},
		BAList: &filter.Rules{
			DoDBs: []string{"test"},
		},
	}
	cfg.From.Adjust()
	cfg.To.Adjust()
	ret.cfg = cfg
	return ret
}

func putMasterMeta(ctx context.Context, t *testing.T, metaclient metadata.MetaKV, metaData *lib.MasterMetaKVData) {
	masterKey := adapter.MasterInfoKey.Encode("master-id")
	masterInfoBytes, err := json.Marshal(metaData)
	require.NoError(t, err)
	_, err = metaclient.Put(ctx, masterKey, string(masterInfoBytes))
	require.NoError(t, err)
}

func TestDumpWorker(t *testing.T) {
	t.Parallel()

	require.NoError(t, log.InitLogger(&log.Config{Level: "debug"}))
	ctx, cancel := context.WithCancel(context.Background())
	worker := mockWorker("worker-id", "master-id")

	putMasterMeta(context.Background(), t, worker.MetaKVClient(), &lib.MasterMetaKVData{
		ID:          "master-id",
		NodeID:      "mode-id",
		Epoch:       1,
		Initialized: true,
	})

	err := worker.Init(ctx)
	require.NoError(t, err)
	err = worker.Tick(ctx)
	require.NoError(t, err)
	utils.WaitSomething(10, 100*time.Millisecond, func() bool {
		status, err := worker.Status()
		require.NoError(t, err)
		return status.Code == lib.WorkerStatusError || status.Code == lib.WorkerStatusFinished
	})
	cancel()
	status, err := worker.Status()
	require.NoError(t, err)
	require.Equal(t, lib.WorkerStatusFinished, status.Code)
	worker.Close()
}
