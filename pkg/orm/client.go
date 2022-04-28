package orm

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	dmysql "github.com/go-sql-driver/mysql"
	libModel "github.com/hanfei1991/microcosm/lib/model"
	cerrors "github.com/hanfei1991/microcosm/pkg/errors"
	resourcemeta "github.com/hanfei1991/microcosm/pkg/externalresource/resourcemeta/model"
	"github.com/hanfei1991/microcosm/pkg/meta/metaclient"
	"github.com/hanfei1991/microcosm/pkg/orm/model"
	"github.com/hanfei1991/microcosm/pkg/tenant"
	"github.com/pingcap/log"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TODO: retry and idempotent??
// TODO: context control??

type TimeRange struct {
	start time.Time
	end   time.Time
}

type Client interface {
	metaclient.Client

	// Initialize will create all tables for backend operation
	Initialize(ctx context.Context) error

	// project
	AddProject(ctx context.Context, project *model.ProjectInfo) error
	DeleteProject(ctx context.Context, projectID string) error
	QueryProjects(ctx context.Context) ([]*model.ProjectInfo, error)
	GetProjectByID(ctx context.Context, projectID string) (*model.ProjectInfo, error)

	// project operation
	AddProjectOperation(ctx context.Context, op *model.ProjectOperation) error
	QueryProjectOperations(ctx context.Context, projectID string) ([]*model.ProjectOperation, error)
	QueryProjectOperationsByTimeRange(ctx context.Context, projectID string, tr TimeRange) ([]*model.ProjectOperation, error)

	// job info
	UpsertJob(ctx context.Context, job *libModel.MasterMetaKVData) error
	UpdateJob(ctx context.Context, job *libModel.MasterMetaKVData) error
	DeleteJob(ctx context.Context, jobID string) error
	GetJobByID(ctx context.Context, jobID string) (*libModel.MasterMetaKVData, error)
	QueryJobs(ctx context.Context) ([]*libModel.MasterMetaKVData, error)
	QueryJobsByProjectID(ctx context.Context, projectID string) ([]*libModel.MasterMetaKVData, error)
	QueryJobsByStatus(ctx context.Context, jobID string, status int) ([]*libModel.MasterMetaKVData, error)

	// worker status
	UpsertWorker(ctx context.Context, worker *libModel.WorkerStatus) error
	UpdateWorker(ctx context.Context, worker *libModel.WorkerStatus) error
	DeleteWorker(ctx context.Context, masterID string, workerID string) error
	GetWorkerByID(ctx context.Context, masterID string, workerID string) (*libModel.WorkerStatus, error)
	QueryWorkersByMasterID(ctx context.Context, masterID string) ([]*libModel.WorkerStatus, error)
	QueryWorkersByStatus(ctx context.Context, masterID string, status int) ([]*libModel.WorkerStatus, error)

	// resource meta
	UpsertResource(ctx context.Context, resource *resourcemeta.ResourceMeta) error
	UpdateResource(ctx context.Context, resource *resourcemeta.ResourceMeta) error
	DeleteResource(ctx context.Context, resourceID string) error
	GetResourceByID(ctx context.Context, resourceID string) (*resourcemeta.ResourceMeta, error)
	QueryResources(ctx context.Context) ([]*resourcemeta.ResourceMeta, error)
	QueryResourcesByJobID(ctx context.Context, jobID string) ([]*resourcemeta.ResourceMeta, error)
	QueryResourcesByExecutorID(ctx context.Context, executorID string) ([]*resourcemeta.ResourceMeta, error)
}

// NewMetaOpsClient return the client to operate framework metastore
func NewClient(mc metaclient.StoreConfigParams, projectID tenant.ProjectID, conf DBConfig) (Client, error) {
	err := createDatabaseForProject(mc, projectID, conf)
	if err != nil {
		return nil, err
	}

	dsn := generateDSNByParams(mc, projectID, conf, true)
	sqlDB, err := newSQLDB("mysql", dsn, conf)
	if err != nil {
		return nil, err
	}

	cli, err := newClient(sqlDB)
	if err != nil {
		sqlDB.Close()
	}

	return cli, err
}

// TODO: check the projectID
func createDatabaseForProject(mc metaclient.StoreConfigParams, projectID tenant.ProjectID, conf DBConfig) error {
	dsn := generateDSNByParams(mc, projectID, conf, false)
	log.L().Info("mysql connection", zap.String("dsn", dsn))

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.L().Error("open dsn fail", zap.String("dsn", dsn), zap.Error(err))
		return cerrors.ErrMetaOpFail.Wrap(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	query := fmt.Sprintf("CREATE DATABASE if not exists %s", projectID)
	_, err = db.ExecContext(ctx, query)
	if err != nil {
		return cerrors.ErrMetaOpFail.Wrap(err)
	}

	return nil
}

// generateDSNByParams will use projectID as DBName to achieve isolation.
// Besides, it will add some default mysql params to the dsn
func generateDSNByParams(mc metaclient.StoreConfigParams, projectID tenant.ProjectID,
	conf DBConfig, withDB bool,
) string {
	dsnCfg := dmysql.NewConfig()
	if dsnCfg.Params == nil {
		dsnCfg.Params = make(map[string]string, 1)
	}
	dsnCfg.User = mc.User
	dsnCfg.Passwd = mc.Password
	dsnCfg.Net = "tcp"
	dsnCfg.Addr = mc.Endpoints[0]
	if withDB {
		dsnCfg.DBName = projectID
	}
	dsnCfg.InterpolateParams = true
	// dsnCfg.MultiStatements = true
	dsnCfg.Params["readTimeout"] = conf.ReadTimeout
	dsnCfg.Params["writeTimeout"] = conf.WriteTimeout
	dsnCfg.Params["timeout"] = conf.DialTimeout
	dsnCfg.Params["parseTime"] = "true"
	// TODO: check for timezone
	dsnCfg.Params["loc"] = "Local"

	// dsn format: [username[:password]@][protocol[(address)]]/
	return dsnCfg.FormatDSN()
}

// newSqlDB return sql.DB for specified driver and dsn
func newSQLDB(driver string, dsn string, conf DBConfig) (*sql.DB, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		log.L().Error("open dsn fail", zap.String("dsn", dsn), zap.Any("config", conf), zap.Error(err))
		return nil, cerrors.ErrMetaOpFail.Wrap(err)
	}

	db.SetConnMaxIdleTime(conf.ConnMaxIdleTime)
	db.SetConnMaxLifetime(conf.ConnMaxLifeTime)
	db.SetMaxIdleConns(conf.MaxIdleConns)
	db.SetMaxOpenConns(conf.MaxOpenConns)
	return db, nil
}

func newClient(sqlDB *sql.DB) (*metaOpsClient, error) {
	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: false,
	}), &gorm.Config{
		SkipDefaultTransaction: true,
		// TODO: logger
	})
	if err != nil {
		log.L().Error("create gorm client fail", zap.Error(err))
		return nil, err
	}

	return &metaOpsClient{
		db:   db,
		impl: sqlDB,
	}, nil
}

// metaOpsClient is the meta operations client for framework metastore
type metaOpsClient struct {
	// gorm claim to be thread safe
	db   *gorm.DB
	impl *sql.DB
}

func (c *metaOpsClient) Close() error {
	if c.impl != nil {
		return c.impl.Close()
	}

	return nil
}

////////////////////////// Initialize
// Initialize will create all related tables in SQL backend
// TODO: What if we change the definition of orm??
func (c *metaOpsClient) Initialize(ctx context.Context) error {
	if err := c.db.AutoMigrate(&model.ProjectInfo{}, &model.ProjectOperation{}, &libModel.MasterMetaKVData{},
		&libModel.WorkerStatus{}, &resourcemeta.ResourceMeta{}, &model.LogicEpoch{}); err != nil {
		return cerrors.ErrMetaOpFail.Wrap(err)
	}

	// check first record in logic_epochs
	return c.InitializeEpoch(ctx)
}

/////////////////////////////// Logic Epoch
// TODO: what if the record is deleted manually??
func (c *metaOpsClient) InitializeEpoch(ctx context.Context) error {
	var logicEp model.LogicEpoch
	// first check and add first record if not exists
	if result := c.db.First(&logicEp, defaultEpochPK); result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			if res := c.db.Create(&model.LogicEpoch{
				Model: model.Model{
					SeqID: defaultEpochPK,
				},
				Epoch: defaultMinEpoch,
			}); res.Error != nil {
				return cerrors.ErrMetaOpFail.Wrap(res.Error)
			}

			return nil
		}

		return cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	// already exists, do nothing
	return nil
}

func (c *metaOpsClient) GenEpoch(ctx context.Context) (libModel.Epoch, error) {
	var epoch libModel.Epoch
	err := c.db.Transaction(func(tx *gorm.DB) error {
		//(1)update epoch = epoch + 1
		if err := tx.Model(&model.LogicEpoch{
			Model: model.Model{
				SeqID: defaultEpochPK,
			},
		}).Update("epoch", gorm.Expr("epoch + ?", 1)).Error; err != nil {
			// return any error will rollback
			return err
		}

		//(2)select epoch
		var logicEp model.LogicEpoch
		if err := tx.First(&logicEp, defaultEpochPK).Error; err != nil {
			return err
		}
		epoch = libModel.Epoch(logicEp.Epoch)

		// return nil will commit the whole transaction
		return nil
	})
	if err != nil {
		return libModel.Epoch(0), cerrors.ErrMetaOpFail.Wrap(err)
	}

	return epoch, nil
}

///////////////////////// Project Operation
// AddProject insert the model.ProjectInfo
func (c *metaOpsClient) AddProject(ctx context.Context, project *model.ProjectInfo) error {
	if project == nil {
		return cerrors.ErrMetaOpFail.GenWithStackByArgs("input project info is nil")
	}
	if result := c.db.Create(project); result.Error != nil {
		return cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return nil
}

// DeleteProject delete the model.ProjectInfo
func (c *metaOpsClient) DeleteProject(ctx context.Context, projectID string) error {
	if result := c.db.Where("id=?", projectID).Delete(&model.ProjectInfo{}); result.Error != nil {
		return cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return nil
}

// QueryProject query all projects
func (c *metaOpsClient) QueryProjects(ctx context.Context) ([]*model.ProjectInfo, error) {
	var projects []*model.ProjectInfo
	if result := c.db.Find(&projects); result.Error != nil {
		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return projects, nil
}

// GetProjectByID query project by projectID
func (c *metaOpsClient) GetProjectByID(ctx context.Context, projectID string) (*model.ProjectInfo, error) {
	var project model.ProjectInfo
	if result := c.db.Where("id = ?", projectID).First(&project); result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, cerrors.ErrMetaEntryNotFound.Wrap(result.Error)
		}

		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return &project, nil
}

// AddProjectOperation insert the operation
func (c *metaOpsClient) AddProjectOperation(ctx context.Context, op *model.ProjectOperation) error {
	if op == nil {
		return cerrors.ErrMetaOpFail.GenWithStackByArgs("input project operation is nil")
	}

	if result := c.db.Create(op); result.Error != nil {
		return cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return nil
}

// QueryProjectOperations query all operations of the projectID
func (c *metaOpsClient) QueryProjectOperations(ctx context.Context, projectID string) ([]*model.ProjectOperation, error) {
	var projectOps []*model.ProjectOperation
	if result := c.db.Where("project_id = ?", projectID).Find(&projectOps); result.Error != nil {
		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return projectOps, nil
}

// QueryProjectOperationsByTimeRange query project operation betweem a time range of the projectID
func (c *metaOpsClient) QueryProjectOperationsByTimeRange(ctx context.Context,
	projectID string, tr TimeRange,
) ([]*model.ProjectOperation, error) {
	var projectOps []*model.ProjectOperation
	if result := c.db.Where("project_id = ? AND created_at >= ? AND created_at <= ?", projectID, tr.start,
		tr.end).Find(&projectOps); result.Error != nil {
		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return projectOps, nil
}

/////////////////////////////// Job Operation
// UpsertJob upsert the jobInfo
// TODO: refine me
func (c *metaOpsClient) UpsertJob(ctx context.Context, job *libModel.MasterMetaKVData) error {
	if job == nil {
		return cerrors.ErrMetaOpFail.GenWithStackByArgs("input master meta is nil")
	}

	if err := c.db.Create(job).Error; err != nil {
		if !isDuplicateEntryErr(err) {
			return cerrors.ErrMetaOpFail.Wrap(err)
		}
		if err := c.UpdateJob(ctx, job); err != nil {
			return err
		}
	}

	return nil
}

// UpdateJob update the jobInfo
func (c *metaOpsClient) UpdateJob(ctx context.Context, job *libModel.MasterMetaKVData) error {
	if job == nil {
		return cerrors.ErrMetaOpFail.GenWithStackByArgs("input master meta is nil")
	}
	// we don't use `Save` here to avoid user dealing with the basic model
	// expected SQL: UPDATE xxx SET xxx='xxx', updated_at='2013-11-17 21:34:10' WHERE id=xxx;
	if err := c.db.Model(&libModel.MasterMetaKVData{}).Where("id = ?", job.ID).Updates(job.Map()).Error; err != nil {
		return cerrors.ErrMetaOpFail.Wrap(err)
	}

	return nil
}

// DeleteJob delete the specified jobInfo
func (c *metaOpsClient) DeleteJob(ctx context.Context, jobID string) error {
	if result := c.db.Where("id = ?", jobID).Delete(&libModel.MasterMetaKVData{}); result.Error != nil {
		return cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return nil
}

// GetJobByID query job by `jobID`
func (c *metaOpsClient) GetJobByID(ctx context.Context, jobID string) (*libModel.MasterMetaKVData, error) {
	var job libModel.MasterMetaKVData
	if result := c.db.Where("id = ?", jobID).First(&job); result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, cerrors.ErrMetaEntryNotFound.Wrap(result.Error)
		}

		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return &job, nil
}

// QueryJobsByProjectID query all jobs of projectID
func (c *metaOpsClient) QueryJobs(ctx context.Context) ([]*libModel.MasterMetaKVData, error) {
	var jobs []*libModel.MasterMetaKVData
	if result := c.db.Find(&jobs); result.Error != nil {
		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return jobs, nil
}

// QueryJobsByProjectID query all jobs of projectID
func (c *metaOpsClient) QueryJobsByProjectID(ctx context.Context, projectID string) ([]*libModel.MasterMetaKVData, error) {
	var jobs []*libModel.MasterMetaKVData
	if result := c.db.Where("project_id = ?", projectID).Find(&jobs); result.Error != nil {
		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return jobs, nil
}

// QueryJobsByStatus query all jobs with `status` of the projectID
func (c *metaOpsClient) QueryJobsByStatus(ctx context.Context,
	jobID string, status int,
) ([]*libModel.MasterMetaKVData, error) {
	var jobs []*libModel.MasterMetaKVData
	if result := c.db.Where("id = ? AND status = ?", jobID, status).Find(&jobs); result.Error != nil {
		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return jobs, nil
}

/////////////////////////////// Worker Operation
// AddWorker insert the workerInfo
// TODO: refine me
func (c *metaOpsClient) UpsertWorker(ctx context.Context, worker *libModel.WorkerStatus) error {
	if worker == nil {
		return cerrors.ErrMetaOpFail.GenWithStackByArgs("input worker meta is nil")
	}

	if err := c.db.Create(worker).Error; err != nil {
		if !isDuplicateEntryErr(err) {
			return cerrors.ErrMetaOpFail.Wrap(err)
		}

		if err := c.UpdateWorker(ctx, worker); err != nil {
			return err
		}
	}

	return nil
}

func (c *metaOpsClient) UpdateWorker(ctx context.Context, worker *libModel.WorkerStatus) error {
	if worker == nil {
		return cerrors.ErrMetaOpFail.GenWithStackByArgs("input worker meta is nil")
	}
	// we don't use `Save` here to avoid user dealing with the basic model
	if err := c.db.Model(&libModel.WorkerStatus{}).Where("job_id = ? && id = ?", worker.JobID, worker.ID).Updates(worker.Map()).Error; err != nil {
		return cerrors.ErrMetaOpFail.Wrap(err)
	}

	return nil
}

// DeleteWorker delete the specified workInfo
func (c *metaOpsClient) DeleteWorker(ctx context.Context, masterID string, workerID string) error {
	if result := c.db.Where("job_id = ? AND id = ?", masterID,
		workerID).Delete(&libModel.WorkerStatus{}); result.Error != nil {
		return cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return nil
}

// GetWorkerByID query worker info by workerID
func (c *metaOpsClient) GetWorkerByID(ctx context.Context, masterID string, workerID string) (*libModel.WorkerStatus, error) {
	var worker libModel.WorkerStatus
	if result := c.db.Where("job_id = ? AND id = ?", masterID,
		workerID).First(&worker); result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, cerrors.ErrMetaEntryNotFound.Wrap(result.Error)
		}

		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return &worker, nil
}

// QueryWorkersByMasterID query all workers of masterID
func (c *metaOpsClient) QueryWorkersByMasterID(ctx context.Context, masterID string) ([]*libModel.WorkerStatus, error) {
	var workers []*libModel.WorkerStatus
	if result := c.db.Where("job_id = ?", masterID).Find(&workers); result.Error != nil {
		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return workers, nil
}

// QueryWorkersByStatus query all workers with specified status of masterID
func (c *metaOpsClient) QueryWorkersByStatus(ctx context.Context, masterID string, status int) ([]*libModel.WorkerStatus, error) {
	var workers []*libModel.WorkerStatus
	if result := c.db.Where("job_id = ? AND status = ?", masterID,
		status).Find(&workers); result.Error != nil {
		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return workers, nil
}

/////////////////////////////// Resource Operation
// UpsertResource insert the model.libModel.resourcemeta.ResourceMeta
// TODO: refine me
func (c *metaOpsClient) UpsertResource(ctx context.Context, resource *resourcemeta.ResourceMeta) error {
	if resource == nil {
		return cerrors.ErrMetaOpFail.GenWithStackByArgs("input resource meta is nil")
	}

	if err := c.db.Create(resource).Error; err != nil {
		if !isDuplicateEntryErr(err) {
			return cerrors.ErrMetaOpFail.Wrap(err)
		}

		if err := c.UpdateResource(ctx, resource); err != nil {
			return err
		}
	}

	return nil
}

func (c *metaOpsClient) UpdateResource(ctx context.Context, resource *resourcemeta.ResourceMeta) error {
	if resource == nil {
		return cerrors.ErrMetaOpFail.GenWithStackByArgs("input resource meta is nil")
	}
	// we don't use `Save` here to avoid user dealing with the basic model
	if err := c.db.Model(&resourcemeta.ResourceMeta{}).Where("id = ?", resource.ID).Updates(resource.Map()).Error; err != nil {
		return cerrors.ErrMetaOpFail.Wrap(err)
	}

	return nil
}

// DeleteResource delete the specified model.libModel.resourcemeta.ResourceMeta
func (c *metaOpsClient) DeleteResource(ctx context.Context, resourceID string) error {
	if result := c.db.Where("id = ?", resourceID).Delete(&resourcemeta.ResourceMeta{}); result.Error != nil {
		return cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return nil
}

// GetResourceByID query resource of the resource_id
func (c *metaOpsClient) GetResourceByID(ctx context.Context, resourceID string) (*resourcemeta.ResourceMeta, error) {
	var resource resourcemeta.ResourceMeta
	if result := c.db.Where("id = ?", resourceID).First(&resource); result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, cerrors.ErrMetaEntryNotFound.Wrap(result.Error)
		}

		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return &resource, nil
}

func (c *metaOpsClient) QueryResources(ctx context.Context) ([]*resourcemeta.ResourceMeta, error) {
	var resources []*resourcemeta.ResourceMeta
	if result := c.db.Find(&resources); result.Error != nil {
		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return resources, nil
}

// QueryResourcesByJobID query all resources of the jobID
func (c *metaOpsClient) QueryResourcesByJobID(ctx context.Context, jobID string) ([]*resourcemeta.ResourceMeta, error) {
	var resources []*resourcemeta.ResourceMeta
	if result := c.db.Where("job_id = ?", jobID).Find(&resources); result.Error != nil {
		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return resources, nil
}

// QueryResourcesByExecutorID query all resources of the executor_id
func (c *metaOpsClient) QueryResourcesByExecutorID(ctx context.Context, executorID string) ([]*resourcemeta.ResourceMeta, error) {
	var resources []*resourcemeta.ResourceMeta
	if result := c.db.Where("executor_id = ?", executorID).Find(&resources); result.Error != nil {
		return nil, cerrors.ErrMetaOpFail.Wrap(result.Error)
	}

	return resources, nil
}

func isDuplicateEntryErr(err error) bool {
	if errMy, ok := err.(*dmysql.MySQLError); ok {
		if errMy.Number == 1062 {
			return true
		}

		return false
	}

	return false
}