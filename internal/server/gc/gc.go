package gc

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	"github.com/zhenzou/executors"
	"go.uber.org/fx"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channelprobe"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/ent/schema/schematype"
	"github.com/looplj/axonhub/internal/ent/thread"
	"github.com/looplj/axonhub/internal/ent/trace"
	"github.com/looplj/axonhub/internal/ent/usagelog"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
)

// defaultBatchSize is the default batch size for cleanup operations
// This can be overridden for testing.
var defaultBatchSize = 500

type Config struct {
	CRON          string `json:"cron" yaml:"cron" conf:"cron" validate:"required"`
	VacuumEnabled bool   `json:"vacuum_enabled" yaml:"vacuum_enabled" conf:"vacuum_enabled"`
	VacuumFull    bool   `json:"vacuum_full" yaml:"vacuum_full" conf:"vacuum_full"`
}

// Worker handles garbage collection and cleanup operations.
type Worker struct {
	SystemService      *biz.SystemService
	DataStorageService *biz.DataStorageService
	Executor           executors.ScheduledExecutor
	Ent                *ent.Client
	Config             Config
	CancelFunc         context.CancelFunc
}

type Params struct {
	fx.In

	Config             Config
	SystemService      *biz.SystemService
	DataStorageService *biz.DataStorageService
	Client             *ent.Client
}

// NewWorker creates a new GCService with daily cleanup scheduling.
func NewWorker(params Params) *Worker {
	return &Worker{
		SystemService:      params.SystemService,
		DataStorageService: params.DataStorageService,
		Executor:           executors.NewPoolScheduleExecutor(executors.WithMaxConcurrent(1)),
		Ent:                params.Client,
		Config:             params.Config,
	}
}

// deleteInBatches deletes records in batches to avoid memory issues
// This function repeatedly executes the delete query until no more records are deleted.
func (w *Worker) deleteInBatches(ctx context.Context, deleteFunc func() (int, error)) (int, error) {
	totalDeleted := 0

	for {
		// Delete a batch of records
		deleted, err := deleteFunc()
		if err != nil {
			return totalDeleted, fmt.Errorf("failed to delete batch: %w", err)
		}

		if deleted == 0 {
			// No more records to delete
			break
		}

		totalDeleted += deleted
		log.Debug(ctx, "Deleted batch of records", log.Int("batch_size", deleted), log.Int("total_deleted", totalDeleted))
	}

	return totalDeleted, nil
}

// getBatchSize returns the appropriate batch size for cleanup operations
// Returns 10 for test environment, 500 for production.
func (w *Worker) getBatchSize() int {
	// Check if running in test mode by checking context or environment
	// For now, use a default batch size that can be overridden via config if needed
	// In production, this should return 500
	// In tests, it can be overridden to 10
	return defaultBatchSize
}

func (w *Worker) Start(ctx context.Context) error {
	cancelFunc, err := w.Executor.ScheduleFuncAtCronRate(
		w.runCleanupWithSystemContext,
		executors.CRONRule{Expr: w.Config.CRON},
	)
	if err != nil {
		return err
	}

	w.CancelFunc = cancelFunc

	log.Info(ctx, "GC worker started", log.String("cron", w.Config.CRON),
		log.Bool("cancel_func", w.CancelFunc != nil),
		log.Bool("ent", w.Ent != nil),
		log.Bool("executor", w.Executor != nil),
		log.Bool("system_service", w.SystemService != nil),
	)

	return nil
}

func (w *Worker) Stop(ctx context.Context) error {
	if w.CancelFunc != nil {
		w.CancelFunc()
	}

	return w.Executor.Shutdown(ctx)
}

// runCleanup executes the cleanup process based on storage policy.
func (w *Worker) runCleanup(ctx context.Context, manual bool) {
	log.Info(ctx, "Starting automatic cleanup process")

	ctx = ent.NewContext(ctx, w.Ent)
	ctx = schematype.SkipSoftDelete(ctx)

	// Get storage policy
	policy, err := w.SystemService.StoragePolicy(ctx)
	if err != nil {
		log.Error(ctx, "Failed to get storage policy for cleanup", log.Cause(err))
		return
	}

	log.Debug(ctx, "Storage policy for cleanup", log.Any("policy", policy))

	// Execute cleanup for each resource type
	for _, option := range policy.CleanupOptions {
		if option.Enabled {
			switch option.ResourceType {
			case "requests":
				err := w.cleanupRequests(ctx, option.CleanupDays, manual)
				if err != nil {
					log.Error(ctx, "Failed to cleanup requests",
						log.String("resource", option.ResourceType),
						log.Cause(err))
				} else {
					log.Info(ctx, "Successfully cleaned up requests",
						log.String("resource", option.ResourceType),
						log.Int("cleanup_days", option.CleanupDays))
				}

				err = w.cleanupThreads(ctx, option.CleanupDays, manual)
				if err != nil {
					log.Error(ctx, "Failed to cleanup threads",
						log.String("resource", "threads"),
						log.Cause(err))
				} else {
					log.Info(ctx, "Successfully cleaned up threads",
						log.String("resource", "threads"),
						log.Int("cleanup_days", option.CleanupDays))
				}

				err = w.cleanupTraces(ctx, option.CleanupDays, manual)
				if err != nil {
					log.Error(ctx, "Failed to cleanup traces",
						log.String("resource", "traces"),
						log.Cause(err))
				} else {
					log.Info(ctx, "Successfully cleaned up traces",
						log.String("resource", "traces"),
						log.Int("cleanup_days", option.CleanupDays))
				}
			case "usage_logs":
				err := w.cleanupUsageLogs(ctx, option.CleanupDays, manual)
				if err != nil {
					log.Error(ctx, "Failed to cleanup usage logs",
						log.String("resource", option.ResourceType),
						log.Cause(err))
				} else {
					log.Info(ctx, "Successfully cleaned up usage logs",
						log.String("resource", option.ResourceType),
						log.Int("cleanup_days", option.CleanupDays))
				}
			case CleanupResourceRequestBodies, CleanupResourceResponseBodies, CleanupResourceResponseChunks:
				// Body-level retention: delete only the externally stored body files,
				// keeping the request/execution records (and their metadata) intact.
				deleted, err := w.cleanupExternalBodies(ctx, option.ResourceType, option.CleanupDays, manual)
				if err != nil {
					log.Error(ctx, "Failed to cleanup external bodies",
						log.String("resource", option.ResourceType),
						log.Cause(err))
				} else {
					log.Info(ctx, "Successfully cleaned up external bodies",
						log.String("resource", option.ResourceType),
						log.Int("cleanup_days", option.CleanupDays),
						log.Int("deleted_files", deleted))
				}
			default:
				log.Warn(ctx, "Unknown resource type for cleanup",
					log.String("resource", option.ResourceType))
			}
		}
	}

	// Always cleanup channel probe data older than 3 days
	err = w.cleanupChannelProbes(ctx, 3, manual)
	if err != nil {
		log.Error(ctx, "Failed to cleanup channel probes",
			log.Cause(err))
	} else {
		log.Info(ctx, "Successfully cleaned up channel probes",
			log.Int("cleanup_days", 3))
	}

	// Run VACUUM after cleanup to reclaim storage space (SQLite and PostgreSQL)
	if w.Config.VacuumEnabled {
		if err := w.runVacuum(ctx); err != nil {
			log.Error(ctx, "Failed to run VACUUM after cleanup",
				log.Cause(err))
		}
	}

	log.Info(ctx, "Automatic cleanup process completed")
}

// cleanupRequests deletes requests older than the specified number of days.
func (w *Worker) cleanupRequests(ctx context.Context, cleanupDays int, manual bool) error {
	if !manual && cleanupDays <= 0 {
		log.Debug(ctx, "No cleanup needed for requests")
		return nil // No cleanup needed
	}

	cutoffTime := time.Now().AddDate(0, 0, -cleanupDays)
	if manual && cleanupDays == 0 {
		cutoffTime = time.Now()
	}

	execResult, err := w.cleanupOldRequestExecutions(ctx, cutoffTime)
	if err != nil {
		return fmt.Errorf("failed to cleanup request executions: %w", err)
	}

	log.Debug(ctx, "Deleted old request executions",
		log.Int("deleted_executions_count", execResult),
		log.Time("cutoff_time", cutoffTime),
	)

	reqResult, err := w.cleanupOldRequestsRecords(ctx, cutoffTime)
	if err != nil {
		return fmt.Errorf("failed to cleanup requests: %w", err)
	}

	log.Debug(ctx, "Deleted old requests",
		log.Int("deleted_requests_count", reqResult),
		log.Time("cutoff_time", cutoffTime))

	return nil
}

func (w *Worker) cleanupOldRequestExecutions(ctx context.Context, cutoffTime time.Time) (int, error) {
	batchSize := w.getBatchSize()
	totalDeleted := 0
	cache := make(map[int]*ent.DataStorage)

	for {
		executions, err := w.Ent.RequestExecution.Query().
			Select(
				requestexecution.FieldID,
				requestexecution.FieldProjectID,
				requestexecution.FieldDataStorageID,
				requestexecution.FieldRequestID,
			).
			Where(requestexecution.CreatedAtLT(cutoffTime)).
			Order(ent.Asc(requestexecution.FieldID)).
			Limit(batchSize).
			All(ctx)
		if err != nil {
			return totalDeleted, fmt.Errorf("failed to query old request executions: %w", err)
		}

		if len(executions) == 0 {
			break
		}

		ids := make([]int, len(executions))

		for i, exec := range executions {
			ids[i] = exec.ID
			w.cleanupExecutionExternalStorage(ctx, exec, cache)
		}

		if _, err := w.Ent.RequestExecution.Delete().
			Where(requestexecution.IDIn(ids...)).
			Exec(ctx); err != nil {
			return totalDeleted, fmt.Errorf("failed to delete request executions batch: %w", err)
		}

		log.Debug(ctx, "Deleted old request executions batch",
			log.Int("deleted_executions_count", len(ids)),
			log.Time("cutoff_time", cutoffTime),
		)

		totalDeleted += len(ids)
	}

	return totalDeleted, nil
}

func (w *Worker) cleanupOldRequestsRecords(ctx context.Context, cutoffTime time.Time) (int, error) {
	batchSize := w.getBatchSize()
	totalDeleted := 0
	cache := make(map[int]*ent.DataStorage)

	for {
		reqs, err := w.Ent.Request.Query().
			Select(
				request.FieldID,
				request.FieldProjectID,
				request.FieldDataStorageID,
			).
			Where(request.CreatedAtLT(cutoffTime)).
			Order(ent.Asc(request.FieldID)).
			Limit(batchSize).
			All(ctx)
		if err != nil {
			return totalDeleted, fmt.Errorf("failed to query old requests: %w", err)
		}

		if len(reqs) == 0 {
			break
		}

		ids := make([]int, len(reqs))
		for i, req := range reqs {
			ids[i] = req.ID
			w.cleanupRequestExternalStorage(ctx, req, cache)
		}

		if _, err := w.Ent.Request.Delete().
			Where(request.IDIn(ids...)).
			Exec(ctx); err != nil {
			return totalDeleted, fmt.Errorf("failed to delete requests batch: %w", err)
		}

		totalDeleted += len(ids)
	}

	return totalDeleted, nil
}

func (w *Worker) cleanupExecutionExternalStorage(ctx context.Context, exec *ent.RequestExecution, cache map[int]*ent.DataStorage) {
	if exec == nil || exec.DataStorageID == 0 || w.DataStorageService == nil {
		return
	}

	ds, err := w.getDataStorageCached(ctx, exec.DataStorageID, cache)
	if err != nil {
		log.Warn(ctx, "Failed to load data storage for execution cleanup",
			log.Cause(err),
			log.Int("execution_id", exec.ID),
		)

		return
	}

	if ds == nil || ds.Primary {
		return
	}

	keys := []string{
		biz.GenerateExecutionRequestBodyKey(exec.ProjectID, exec.RequestID, exec.ID),
		biz.GenerateExecutionResponseBodyKey(exec.ProjectID, exec.RequestID, exec.ID),
		biz.GenerateExecutionResponseChunksKey(exec.ProjectID, exec.RequestID, exec.ID),
		biz.GenerateExecutionRequestDirKey(exec.ProjectID, exec.RequestID, exec.ID),
	}

	for _, key := range keys {
		if err := w.DataStorageService.DeleteData(ctx, ds, key); err != nil {
			log.Warn(ctx, "Failed to delete execution external data",
				log.Cause(err),
				log.Int("execution_id", exec.ID),
				log.String("key", key),
			)
		}
	}
}

func (w *Worker) cleanupRequestExternalStorage(ctx context.Context, req *ent.Request, cache map[int]*ent.DataStorage) {
	if req == nil || req.DataStorageID == 0 || w.DataStorageService == nil {
		return
	}

	ds, err := w.getDataStorageCached(ctx, req.DataStorageID, cache)
	if err != nil {
		log.Warn(ctx, "Failed to load data storage for request cleanup",
			log.Cause(err),
			log.Int("request_id", req.ID),
		)

		return
	}

	if ds == nil || ds.Primary {
		return
	}

	keys := []string{
		biz.GenerateRequestBodyKey(req.ProjectID, req.ID),
		biz.GenerateResponseBodyKey(req.ProjectID, req.ID),
		biz.GenerateResponseChunksKey(req.ProjectID, req.ID),
		biz.GenerateRequestExecutionsDirKey(req.ProjectID, req.ID),
		biz.GenerateRequestDirKey(req.ProjectID, req.ID),
	}

	for _, key := range keys {
		if err := w.DataStorageService.DeleteData(ctx, ds, key); err != nil {
			log.Warn(ctx, "Failed to delete request external data",
				log.Cause(err),
				log.Int("request_id", req.ID),
				log.String("key", key),
			)
		}
	}
}

func (w *Worker) getDataStorageCached(ctx context.Context, id int, cache map[int]*ent.DataStorage) (*ent.DataStorage, error) {
	if ds, ok := cache[id]; ok {
		return ds, nil
	}

	ds, err := w.DataStorageService.GetDataStorageByID(ctx, id)
	if err != nil {
		return nil, err
	}

	cache[id] = ds

	return ds, nil
}

// cleanupUsageLogs deletes usage logs older than the specified number of days.
func (w *Worker) cleanupUsageLogs(ctx context.Context, cleanupDays int, manual bool) error {
	if !manual && cleanupDays <= 0 {
		return nil // No cleanup needed
	}

	cutoffTime := time.Now().AddDate(0, 0, -cleanupDays)
	if manual && cleanupDays == 0 {
		cutoffTime = time.Now()
	}

	// Delete usage logs in batches
	result, err := w.deleteInBatches(ctx, func() (int, error) {
		return w.Ent.UsageLog.Delete().Where(usagelog.CreatedAtLT(cutoffTime)).Exec(ctx)
	})
	if err != nil {
		return fmt.Errorf("failed to delete old usage logs: %w", err)
	}

	log.Debug(ctx, "Cleaned up usage logs",
		log.Int("deleted_count", result),
		log.Time("cutoff_time", cutoffTime))

	return nil
}

// cleanupThreads deletes threads older than the specified number of days.
func (w *Worker) cleanupThreads(ctx context.Context, cleanupDays int, manual bool) error {
	if !manual && cleanupDays <= 0 {
		log.Debug(ctx, "No cleanup needed for threads")
		return nil // No cleanup needed
	}

	cutoffTime := time.Now().AddDate(0, 0, -cleanupDays)
	if manual && cleanupDays == 0 {
		cutoffTime = time.Now()
	}

	// Delete threads in batches
	result, err := w.deleteInBatches(ctx, func() (int, error) {
		return w.Ent.Thread.Delete().Where(thread.CreatedAtLT(cutoffTime)).Exec(ctx)
	})
	if err != nil {
		return fmt.Errorf("failed to delete old threads: %w", err)
	}

	log.Debug(ctx, "Cleaned up threads",
		log.Int("deleted_count", result),
		log.Time("cutoff_time", cutoffTime))

	return nil
}

// cleanupTraces deletes traces older than the specified number of days.
func (w *Worker) cleanupTraces(ctx context.Context, cleanupDays int, manual bool) error {
	if !manual && cleanupDays <= 0 {
		log.Debug(ctx, "No cleanup needed for traces")
		return nil // No cleanup needed
	}

	cutoffTime := time.Now().AddDate(0, 0, -cleanupDays)
	if manual && cleanupDays == 0 {
		cutoffTime = time.Now()
	}

	// Delete traces in batches
	result, err := w.deleteInBatches(ctx, func() (int, error) {
		return w.Ent.Trace.Delete().Where(trace.CreatedAtLT(cutoffTime)).Exec(ctx)
	})
	if err != nil {
		return fmt.Errorf("failed to delete old traces: %w", err)
	}

	log.Debug(ctx, "Cleaned up traces",
		log.Int("deleted_count", result),
		log.Time("cutoff_time", cutoffTime))

	return nil
}

// cleanupChannelProbes deletes channel probes older than the specified number of days.
func (w *Worker) cleanupChannelProbes(ctx context.Context, cleanupDays int, manual bool) error {
	if !manual && cleanupDays <= 0 {
		log.Debug(ctx, "No cleanup needed for channel probes")
		return nil // No cleanup needed
	}

	cutoffTime := time.Now().AddDate(0, 0, -cleanupDays)
	if manual && cleanupDays == 0 {
		cutoffTime = time.Now()
	}

	result, err := w.deleteInBatches(ctx, func() (int, error) {
		return w.Ent.ChannelProbe.Delete().Where(channelprobe.TimestampLT(cutoffTime.Unix())).Exec(ctx)
	})
	if err != nil {
		return fmt.Errorf("failed to delete old channel probes: %w", err)
	}

	log.Debug(ctx, "Cleaned up channel probes",
		log.Int("deleted_count", result),
		log.Time("cutoff_time", cutoffTime))

	return nil
}

// runVacuum executes VACUUM command on SQLite/PostgreSQL database to reclaim storage space.
// This should be called after cleanup operations to defragment the database file.
func (w *Worker) runVacuum(ctx context.Context) error {
	if !w.Config.VacuumEnabled {
		log.Debug(ctx, "VACUUM is disabled, skipping")
		return nil
	}

	// Get the underlying SQL driver to check if it's SQLite
	dbDriver := w.Ent.Driver()
	if dbDriver == nil {
		return fmt.Errorf("failed to get database driver")
	}

	// Try to cast to *entsql.Driver to access underlying *sql.DB
	sqlDriver, ok := dbDriver.(*entsql.Driver)
	if !ok {
		log.Debug(ctx, "Database driver is not *entsql.Driver, skipping VACUUM")
		return nil
	}

	// Check if this is SQLite or PostgreSQL
	if sqlDriver.Dialect() != dialect.SQLite && sqlDriver.Dialect() != dialect.Postgres {
		log.Debug(ctx, "Database does not support VACUUM, skipping",
			log.String("dialect", sqlDriver.Dialect()))

		return nil
	}

	log.Info(ctx, "Starting database VACUUM operation",
		log.String("dialect", sqlDriver.Dialect()),
		log.Bool("vacuum_full", w.Config.VacuumFull))

	startTime := time.Now()

	// Execute VACUUM using raw SQL
	var vacuumSQL string
	if sqlDriver.Dialect() == dialect.Postgres && w.Config.VacuumFull {
		vacuumSQL = "VACUUM FULL"
	} else {
		vacuumSQL = "VACUUM"
	}

	if _, err := sqlDriver.ExecContext(ctx, vacuumSQL); err != nil {
		return fmt.Errorf("failed to execute %s: %w", vacuumSQL, err)
	}

	duration := time.Since(startTime)
	log.Info(ctx, "Database VACUUM completed successfully",
		log.Duration("duration", duration),
		log.String("command", vacuumSQL))

	return nil
}

// RunVacuumNow manually triggers the VACUUM operation.
// This can be useful for testing or manual execution.
func (w *Worker) RunVacuumNow(ctx context.Context) error {
	return w.runVacuum(ctx)
}

// RunCleanupNow manually triggers the cleanup process.
// This can be useful for testing or manual execution.
func (w *Worker) RunCleanupNow(ctx context.Context) error {
	w.runCleanup(ctx, true)
	return nil
}

// Cleanup resource types for body-level retention. They delete only externally
// stored body files and keep the request/execution records intact, so operators
// can expire payloads faster than the request log itself.
const (
	CleanupResourceRequestBodies  = "request_bodies"
	CleanupResourceResponseBodies = "response_bodies"
	CleanupResourceResponseChunks = "response_chunks"
)

// bodyCleanupBatchSize bounds how many parent requests one cleanup pass handles
// per query. Kept smaller than the table cleanup batch because each parent may
// trigger extra per-execution file deletions.
var bodyCleanupBatchSize = 200

// cleanupExternalBodies deletes externally stored bodies older than cleanupDays
// while keeping the database records. It walks a moving cursor so a daily run only
// inspects the newly expired time range instead of the whole history.
//
// It returns the number of storage keys it attempted to delete.
func (w *Worker) cleanupExternalBodies(ctx context.Context, resourceType string, cleanupDays int, manual bool) (int, error) {
	if !manual && cleanupDays <= 0 {
		return 0, nil
	}

	if w.DataStorageService == nil {
		return 0, nil
	}

	cutoff := time.Now().AddDate(0, 0, -cleanupDays)
	if manual && cleanupDays == 0 {
		cutoff = time.Now()
	}

	// Resume from the previous cursor so each run only covers the newly expired
	// window. On the very first run the cursor is zero and there is no lower bound:
	// we must walk the whole history below the cutoff, otherwise bodies older than
	// one extra day would never be reclaimed.
	from, err := w.SystemService.BodyCleanupCursor(ctx, resourceType)
	if err != nil {
		log.Warn(ctx, "Failed to read body cleanup cursor, treating as first run",
			log.Cause(err),
			log.String("resource", resourceType))

		from = time.Time{}
	}

	if !from.IsZero() && !from.Before(cutoff) {
		log.Debug(ctx, "No new body retention window to process",
			log.String("resource", resourceType),
			log.Time("cursor", from),
			log.Time("cutoff", cutoff))

		return 0, nil
	}

	log.Info(ctx, "Purging external bodies",
		log.String("resource", resourceType),
		log.Time("from", from),
		log.Time("cutoff", cutoff))

	cache := make(map[int]*ent.DataStorage)
	lastID := 0
	attempted := 0

	for {
		query := w.Ent.Request.Query().
			Select(
				request.FieldID,
				request.FieldProjectID,
				request.FieldDataStorageID,
			).
			Where(
				request.CreatedAtLT(cutoff),
				request.IDGT(lastID),
				request.DataStorageIDNEQ(0),
			)

		if !from.IsZero() {
			query = query.Where(request.CreatedAtGTE(from))
		}

		reqs, err := query.
			Order(ent.Asc(request.FieldID)).
			Limit(bodyCleanupBatchSize).
			All(ctx)
		if err != nil {
			return attempted, fmt.Errorf("failed to query requests for body cleanup: %w", err)
		}

		if len(reqs) == 0 {
			break
		}

		for _, req := range reqs {
			lastID = req.ID

			ds, err := w.getDataStorageCached(ctx, req.DataStorageID, cache)
			if err != nil {
				log.Warn(ctx, "Failed to load data storage for body cleanup",
					log.Cause(err),
					log.Int("request_id", req.ID))

				continue
			}

			// Database-backed storage keeps the body in the row itself; those rows
			// are handled by the "requests" retention option instead.
			if ds == nil || ds.Primary {
				continue
			}

			attempted += w.purgeRequestBodyFiles(ctx, ds, req, resourceType)
		}
	}

	if err := w.SystemService.SetBodyCleanupCursor(ctx, resourceType, cutoff); err != nil {
		log.Warn(ctx, "Failed to persist body cleanup cursor",
			log.Cause(err),
			log.String("resource", resourceType))
	}

	return attempted, nil
}

// purgeRequestBodyFiles deletes the body file(s) of one request that belong to the
// given resource type and returns how many keys were attempted.
func (w *Worker) purgeRequestBodyFiles(ctx context.Context, ds *ent.DataStorage, req *ent.Request, resourceType string) int {
	keys := make([]string, 0, 2)

	switch resourceType {
	case CleanupResourceRequestBodies:
		keys = append(keys, biz.GenerateRequestBodyKey(req.ProjectID, req.ID))

		execs, err := w.Ent.RequestExecution.Query().
			Select(requestexecution.FieldID).
			Where(requestexecution.RequestIDEQ(req.ID)).
			All(ctx)
		if err != nil {
			log.Warn(ctx, "Failed to query executions for request body cleanup",
				log.Cause(err),
				log.Int("request_id", req.ID))
		}

		for _, exec := range execs {
			keys = append(keys, biz.GenerateExecutionRequestBodyKey(req.ProjectID, req.ID, exec.ID))
		}
	case CleanupResourceResponseBodies:
		keys = append(keys, biz.GenerateResponseBodyKey(req.ProjectID, req.ID))
	case CleanupResourceResponseChunks:
		keys = append(keys, biz.GenerateResponseChunksKey(req.ProjectID, req.ID))
	}

	for _, key := range keys {
		if err := w.DataStorageService.DeleteData(ctx, ds, key); err != nil {
			log.Warn(ctx, "Failed to delete external body",
				log.Cause(err),
				log.String("key", key),
				log.String("resource", resourceType))
		}
	}

	return len(keys)
}
