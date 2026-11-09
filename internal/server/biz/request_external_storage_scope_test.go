package biz

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/pkg/xcache"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/datastorage"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
)

// newExternalStorageRequestService builds a RequestService backed by a non-primary
// fs data storage rooted at a temp dir, so the external-storage read path can be
// exercised end to end.
func newExternalStorageRequestService(t *testing.T) (*ent.Client, *RequestService, *ent.DataStorage, string, context.Context) {
	t.Helper()

	client, dataStorageService, ctx := setupDataStorageTest(t)
	t.Cleanup(func() { _ = client.Close() })

	dir := t.TempDir()

	ds, err := client.DataStorage.Create().
		SetName("fs-test").
		SetDescription("fs storage used by external storage scope tests").
		SetPrimary(false).
		SetType(datastorage.TypeFs).
		SetSettings(&objects.DataStorageSettings{Directory: &dir}).
		SetStatus(datastorage.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	service := &RequestService{
		AbstractService:    &AbstractService{db: client},
		DataStorageService: dataStorageService,
	}

	return client, service, ds, dir, ctx
}

// writeStorageFile places data at the storage key the read path will look up.
func writeStorageFile(t *testing.T, root, key string, data []byte) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(key))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o777))
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

// TestLoadRequestBodyResolvesScopeWhenColumnsNotSelected reproduces the entgql
// column-collection trap: a field resolver receives an entity that only carries
// the columns the GraphQL query asked for. Reading data_storage_id / project_id
// off such an entity yields zero, which used to build a key rooted at project 0
// and silently return "{}" instead of the stored body.
func TestLoadRequestBodyResolvesScopeWhenColumnsNotSelected(t *testing.T) {
	client, service, ds, dir, ctx := newExternalStorageRequestService(t)

	const projectID = 7

	req, err := client.Request.Create().
		SetProjectID(projectID).
		SetModelID("deepseek-flash").
		SetStatus(request.StatusCompleted).
		SetDataStorageID(ds.ID).
		SetRequestBody([]byte("{}")).
		Save(ctx)
	require.NoError(t, err)

	payload := []byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"scope probe"}]}`)
	writeStorageFile(t, dir, GenerateRequestBodyKey(projectID, req.ID), payload)

	// Same shape entgql hands to the resolver when the query only selects `id`.
	partial, err := client.Request.Query().
		Where(request.ID(req.ID)).
		Select(request.FieldID).
		Only(ctx)
	require.NoError(t, err)
	require.Zero(t, partial.ProjectID, "precondition: project_id must be unselected")
	require.Zero(t, partial.DataStorageID, "precondition: data_storage_id must be unselected")

	body, err := service.LoadRequestBody(ctx, partial)
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(body))
}

// TestLoadRequestExecutionRequestBodyResolvesScopeWhenColumnsNotSelected covers the
// execution side, which additionally needs request_id to build its storage key.
func TestLoadRequestExecutionRequestBodyResolvesScopeWhenColumnsNotSelected(t *testing.T) {
	client, service, ds, dir, ctx := newExternalStorageRequestService(t)

	const projectID = 7

	req, err := client.Request.Create().
		SetProjectID(projectID).
		SetModelID("deepseek-flash").
		SetStatus(request.StatusCompleted).
		SetDataStorageID(ds.ID).
		SetRequestBody([]byte("{}")).
		Save(ctx)
	require.NoError(t, err)

	exec, err := client.RequestExecution.Create().
		SetProjectID(projectID).
		SetRequestID(req.ID).
		SetModelID("deepseek-flash").
		SetStatus(requestexecution.StatusCompleted).
		SetDataStorageID(ds.ID).
		SetRequestBody([]byte("{}")).
		Save(ctx)
	require.NoError(t, err)

	payload := []byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"execution scope probe"}]}`)
	writeStorageFile(t, dir, GenerateExecutionRequestBodyKey(projectID, req.ID, exec.ID), payload)

	partial, err := client.RequestExecution.Query().
		Where(requestexecution.ID(exec.ID)).
		Select(requestexecution.FieldID).
		Only(ctx)
	require.NoError(t, err)
	require.Zero(t, partial.ProjectID, "precondition: project_id must be unselected")
	require.Zero(t, partial.RequestID, "precondition: request_id must be unselected")
	require.Zero(t, partial.DataStorageID, "precondition: data_storage_id must be unselected")

	body, err := service.LoadRequestExecutionRequestBody(ctx, partial)
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(body))
}

// TestLoadRequestBodyKeepsWorkingWhenColumnsAreSelected makes sure the fix does not
// change behaviour for the normal path where the query selects every column.
func TestLoadRequestBodyKeepsWorkingWhenColumnsAreSelected(t *testing.T) {
	client, service, ds, dir, ctx := newExternalStorageRequestService(t)

	const projectID = 5

	req, err := client.Request.Create().
		SetProjectID(projectID).
		SetModelID("deepseek-flash").
		SetStatus(request.StatusCompleted).
		SetDataStorageID(ds.ID).
		SetRequestBody([]byte("{}")).
		Save(ctx)
	require.NoError(t, err)

	payload := []byte(`{"normal":"path"}`)
	writeStorageFile(t, dir, GenerateRequestBodyKey(projectID, req.ID), payload)

	full, err := client.Request.Get(ctx, req.ID)
	require.NoError(t, err)

	body, err := service.LoadRequestBody(ctx, full)
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(body))
}

// TestUpdateRequestCompletedFallsBackToDatabaseWhenExternalStorageFails locks in
// the fallback: when the external storage cannot be written (disk full, missing
// directory, permissions), the response body must land in the database column
// instead of being dropped on the floor.
func TestUpdateRequestCompletedFallsBackToDatabaseWhenExternalStorageFails(t *testing.T) {
	client, dataStorageService, ctx := setupDataStorageTest(t)
	t.Cleanup(func() { _ = client.Close() })

	systemService := NewSystemService(SystemServiceParams{
		CacheConfig: xcache.Config{
			Mode:   xcache.ModeMemory,
			Memory: xcache.MemoryConfig{Expiration: 5 * time.Minute},
		},
	})

	service := &RequestService{
		AbstractService:    &AbstractService{db: client},
		SystemService:      systemService,
		DataStorageService: dataStorageService,
	}

	// Make the storage root a regular file so directory creation fails no
	// matter which user runs the test (a root container could create /paths).
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))

	unwritable := filepath.Join(blocker, "sub")

	ds, err := client.DataStorage.Create().
		SetName("fs-unwritable").
		SetDescription("fs storage whose directory cannot be created").
		SetPrimary(false).
		SetType(datastorage.TypeFs).
		SetSettings(&objects.DataStorageSettings{Directory: &unwritable}).
		SetStatus(datastorage.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	req, err := client.Request.Create().
		SetProjectID(9).
		SetModelID("deepseek-flash").
		SetStatus(request.StatusCompleted).
		SetDataStorageID(ds.ID).
		SetRequestBody([]byte("{}")).
		Save(ctx)
	require.NoError(t, err)

	payload := []byte(`{"fallback":true,"note":"response body kept in database"}`)

	require.NoError(t, service.UpdateRequestCompleted(ctx, req.ID, "ext-fallback", payload, nil))

	saved, err := client.Request.Get(ctx, req.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(saved.ResponseBody),
		"response body must be stored in the database when the external storage is unavailable")
}

// TestLoadResponseBodyReadsBackTheDatabaseFallback locks in the round trip: when
// the external write fails and the body lands in the database column, the read
// path must find it. Without the read-side fallback the body is written but can
// never be read, which is worse than not writing it at all.
func TestLoadResponseBodyReadsBackTheDatabaseFallback(t *testing.T) {
	client, dataStorageService, ctx := setupDataStorageTest(t)
	t.Cleanup(func() { _ = client.Close() })

	systemService := NewSystemService(SystemServiceParams{
		CacheConfig: xcache.Config{
			Mode:   xcache.ModeMemory,
			Memory: xcache.MemoryConfig{Expiration: 5 * time.Minute},
		},
	})

	service := &RequestService{
		AbstractService:    &AbstractService{db: client},
		SystemService:      systemService,
		DataStorageService: dataStorageService,
	}

	// A regular file as the storage root makes directory creation fail for any user.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))

	unwritable := filepath.Join(blocker, "sub")

	ds, err := client.DataStorage.Create().
		SetName("fs-unwritable-readback").
		SetDescription("fs storage whose directory cannot be created").
		SetPrimary(false).
		SetType(datastorage.TypeFs).
		SetSettings(&objects.DataStorageSettings{Directory: &unwritable}).
		SetStatus(datastorage.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	req, err := client.Request.Create().
		SetProjectID(9).
		SetModelID("deepseek-flash").
		SetStatus(request.StatusProcessing).
		SetDataStorageID(ds.ID).
		SetRequestBody([]byte("{}")).
		Save(ctx)
	require.NoError(t, err)

	payload := []byte(`{"roundtrip":true,"note":"written to the database, read back the same way"}`)

	require.NoError(t, service.UpdateRequestCompleted(ctx, req.ID, "ext-roundtrip", payload, nil))

	fresh, err := client.Request.Get(ctx, req.ID)
	require.NoError(t, err)

	got, err := service.LoadResponseBody(ctx, fresh)
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(got),
		"the read path must return the database fallback instead of an empty body")
}
