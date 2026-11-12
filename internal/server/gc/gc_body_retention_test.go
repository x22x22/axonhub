package gc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

// TestCleanupExternalBodiesFirstRunCoversAllExpired guards a regression where the
// first cleanup pass started from `cutoff-1d` instead of walking the whole history,
// so bodies older than that extra day were never reclaimed.
func TestCleanupExternalBodiesFirstRunCoversAllExpired(t *testing.T) {
	worker, ctx, dataStorage, baseDir := setupWorkerWithFSStorage(t)

	createRequest := func(createdAt time.Time) *ent.Request {
		t.Helper()

		req, err := worker.Ent.Request.Create().
			SetProjectID(11).
			SetModelID("deepseek-flash").
			SetStatus(request.StatusCompleted).
			SetDataStorageID(dataStorage.ID).
			SetRequestBody([]byte("{}")).
			SetCreatedAt(createdAt).
			Save(ctx)
		require.NoError(t, err)

		return req
	}

	now := time.Now()
	veryOld := createRequest(now.AddDate(0, 0, -5))
	justExpired := createRequest(now.AddDate(0, 0, -2))
	fresh := createRequest(now.Add(-1 * time.Hour))

	for _, req := range []*ent.Request{veryOld, justExpired, fresh} {
		createFileForKey(t, baseDir, biz.GenerateRequestBodyKey(req.ProjectID, req.ID))
	}

	deleted, err := worker.cleanupExternalBodies(ctx, CleanupResourceRequestBodies, 1, false)
	require.NoError(t, err)
	require.Equal(t, 2, deleted, "only the two expired requests should be touched, one key each")

	// First run must reach the request that is far older than cutoff-1d.
	assertRemoved(t, baseDir, biz.GenerateRequestBodyKey(veryOld.ProjectID, veryOld.ID))
	assertRemoved(t, baseDir, biz.GenerateRequestBodyKey(justExpired.ProjectID, justExpired.ID))

	// The record itself must survive: body retention only drops payload files.
	stillThere, err := worker.Ent.Request.Get(ctx, veryOld.ID)
	require.NoError(t, err, "request record must be kept after body purge")
	require.Equal(t, veryOld.ID, stillThere.ID)

	// The fresh request keeps its body.
	path := pathForKey(baseDir, biz.GenerateRequestBodyKey(fresh.ProjectID, fresh.ID))
	require.FileExists(t, path)
}

// TestCleanupExternalBodiesIsIncremental verifies the cursor prevents reprocessing
// the same window, and that a second run with no new expired data is a no-op.
func TestCleanupExternalBodiesIsIncremental(t *testing.T) {
	worker, ctx, dataStorage, baseDir := setupWorkerWithFSStorage(t)

	req, err := worker.Ent.Request.Create().
		SetProjectID(12).
		SetModelID("deepseek-flash").
		SetStatus(request.StatusCompleted).
		SetDataStorageID(dataStorage.ID).
		SetRequestBody([]byte("{}")).
		SetCreatedAt(time.Now().AddDate(0, 0, -3)).
		Save(ctx)
	require.NoError(t, err)

	createFileForKey(t, baseDir, biz.GenerateRequestBodyKey(req.ProjectID, req.ID))

	deleted, err := worker.cleanupExternalBodies(ctx, CleanupResourceRequestBodies, 1, false)
	require.NoError(t, err)
	require.Equal(t, 1, deleted)
	assertRemoved(t, baseDir, biz.GenerateRequestBodyKey(req.ProjectID, req.ID))

	// Cursor is now stored; a second immediate run has no new window.
	deleted, err = worker.cleanupExternalBodies(ctx, CleanupResourceRequestBodies, 1, false)
	require.NoError(t, err)
	require.Zero(t, deleted, "second run must not reprocess the same window")
}

// TestCleanupExternalBodiesSkipsPrimaryStorage makes sure database-backed bodies are
// left to the "requests" retention option instead of being deleted twice.
func TestCleanupExternalBodiesSkipsPrimaryStorage(t *testing.T) {
	worker, ctx, _, _ := setupWorkerWithFSStorage(t)

	primary, err := worker.Ent.DataStorage.Create().
		SetName("primary-db").
		SetDescription("primary database storage").
		SetSettings(&objects.DataStorageSettings{}).
		SetPrimary(true).
		SetType("database").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	created, err := worker.Ent.Request.Create().
		SetProjectID(13).
		SetModelID("deepseek-flash").
		SetStatus(request.StatusCompleted).
		SetDataStorageID(primary.ID).
		SetRequestBody([]byte(`{"inline":true}`)).
		SetCreatedAt(time.Now().AddDate(0, 0, -3)).
		Save(ctx)
	require.NoError(t, err)

	deleted, err := worker.cleanupExternalBodies(ctx, CleanupResourceRequestBodies, 1, false)
	require.NoError(t, err)
	require.Zero(t, deleted, "primary storage bodies live in-row and must be skipped")

	row, err := worker.Ent.Request.Get(ctx, created.ID)
	require.NoError(t, err)
	require.NotEmpty(t, row.RequestBody, "in-row body must be untouched")
}

// TestCleanupExternalBodiesRemovesExecutionResponseKeys covers the review finding
// that response bodies and chunks live under per-execution keys, so deleting only
// the parent-request file would leave those payloads behind until the request
// record itself expired.
func TestCleanupExternalBodiesRemovesExecutionResponseKeys(t *testing.T) {
	worker, ctx, dataStorage, baseDir := setupWorkerWithFSStorage(t)

	const projectID = 21

	req, err := worker.Ent.Request.Create().
		SetProjectID(projectID).
		SetModelID("deepseek-flash").
		SetStatus(request.StatusCompleted).
		SetDataStorageID(dataStorage.ID).
		SetRequestBody([]byte("{}")).
		SetCreatedAt(time.Now().AddDate(0, 0, -3)).
		Save(ctx)
	require.NoError(t, err)

	exec, err := worker.Ent.RequestExecution.Create().
		SetRequestID(req.ID).
		SetProjectID(projectID).
		SetModelID("deepseek-flash").
		SetDataStorageID(dataStorage.ID).
		SetStatus(requestexecution.StatusCompleted).
		SetRequestBody([]byte("{}")).
		Save(ctx)
	require.NoError(t, err)

	parentResponse := biz.GenerateResponseBodyKey(projectID, req.ID)
	execResponse := biz.GenerateExecutionResponseBodyKey(projectID, req.ID, exec.ID)
	execChunks := biz.GenerateExecutionResponseChunksKey(projectID, req.ID, exec.ID)

	createFileForKey(t, baseDir, parentResponse)
	createFileForKey(t, baseDir, execResponse)
	createFileForKey(t, baseDir, execChunks)

	deleted, err := worker.cleanupExternalBodies(ctx, CleanupResourceResponseBodies, 1, false)
	require.NoError(t, err)
	require.Equal(t, 2, deleted, "parent response body plus execution response body")

	assertRemoved(t, baseDir, parentResponse)
	assertRemoved(t, baseDir, execResponse)

	// Chunks belong to a different resource type and must survive this pass.
	require.FileExists(t, pathForKey(baseDir, execChunks))
}
