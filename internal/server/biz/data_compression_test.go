package biz

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/datastorage"
	"github.com/looplj/axonhub/internal/objects"
)

// newFSDataStorageForTest creates a non-primary fs data storage rooted at a temp dir.
func newFSDataStorageForTest(t *testing.T, client *ent.Client, ctx context.Context) (*ent.DataStorage, string) {
	t.Helper()

	dir := t.TempDir()
	dirCopy := dir

	ds, err := client.DataStorage.Create().
		SetName("fs-compression-test").
		SetDescription("compression test storage").
		SetPrimary(false).
		SetType(datastorage.TypeFs).
		SetSettings(&objects.DataStorageSettings{Directory: &dirCopy}).
		SetStatus(datastorage.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	return ds, dir
}

func TestIsZstdFrame(t *testing.T) {
	t.Run("empty and short inputs", func(t *testing.T) {
		require.False(t, isZstdFrame(nil))
		require.False(t, isZstdFrame([]byte{}))
		require.False(t, isZstdFrame([]byte{0x28}))
		require.False(t, isZstdFrame([]byte{0x28, 0xB5, 0x2F}))
	})

	t.Run("json is not a frame", func(t *testing.T) {
		require.False(t, isZstdFrame([]byte(`{"model":"deepseek"}`)))
	})

	t.Run("real frame is detected", func(t *testing.T) {
		compressed := compressZstd([]byte(`{"model":"deepseek"}`))
		require.True(t, isZstdFrame(compressed))
		require.Equal(t, byte(0x28), compressed[0])
		require.Equal(t, byte(0xB5), compressed[1])
		require.Equal(t, byte(0x2F), compressed[2])
		require.Equal(t, byte(0xFD), compressed[3])
	})
}

func TestSaveDataWithOptionsRoundTrip(t *testing.T) {
	client, svc, ctx := setupDataStorageTest(t)
	ds, dir := newFSDataStorageForTest(t, client, ctx)

	// A realistic body-ish payload well above the compression threshold.
	payload := []byte(`{"model":"deepseek-v4.1-flash","messages":[` +
		strings.Repeat(`{"role":"user","content":"hello world"},`, 500) +
		`{"role":"user","content":"done"}]}`)
	require.Greater(t, len(payload), DefaultMinCompressSize)

	key := "8/requests/1001/request_body.json"
	require.NoError(t, svc.SaveDataWithOptions(ctx, ds, key, payload, SaveOptions{Compress: true}))

	// The bytes on disk must be a zstd frame and smaller than the input.
	onDisk, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(key)))
	require.NoError(t, err)
	require.True(t, isZstdFrame(onDisk), "stored payload should be a zstd frame")
	require.Less(t, len(onDisk), len(payload), "compressed payload should be smaller")

	// Reading goes through the transparent decompression path.
	got, err := svc.LoadData(ctx, ds, key)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestSaveDataWithOptionsSkipsSmallPayloads(t *testing.T) {
	client, svc, ctx := setupDataStorageTest(t)
	ds, dir := newFSDataStorageForTest(t, client, ctx)

	small := []byte(`{"a":1}`)
	key := "8/requests/1002/response_body.json"

	require.NoError(t, svc.SaveDataWithOptions(ctx, ds, key, small, SaveOptions{Compress: true}))

	onDisk, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(key)))
	require.NoError(t, err)
	require.False(t, isZstdFrame(onDisk), "small payloads must not be wrapped in a frame")

	got, err := svc.LoadData(ctx, ds, key)
	require.NoError(t, err)
	require.Equal(t, small, got)
}

func TestSaveDataWithOptionsRespectsMinSizeOverride(t *testing.T) {
	client, svc, ctx := setupDataStorageTest(t)
	ds, dir := newFSDataStorageForTest(t, client, ctx)

	payload := bytes.Repeat([]byte("x"), 2048)
	key := "8/requests/1003/request_body.json"

	// Force compression for a payload below the default threshold.
	require.NoError(t, svc.SaveDataWithOptions(ctx, ds, key, payload, SaveOptions{
		Compress:        true,
		MinCompressSize: 1,
	}))

	onDisk, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(key)))
	require.NoError(t, err)
	require.True(t, isZstdFrame(onDisk))

	got, err := svc.LoadData(ctx, ds, key)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestSaveDataWithOptionsDoesNotDoubleCompress(t *testing.T) {
	client, svc, ctx := setupDataStorageTest(t)
	ds, dir := newFSDataStorageForTest(t, client, ctx)

	payload := bytes.Repeat([]byte("abc"), 4000)
	alreadyCompressed := compressZstd(payload)

	key := "8/requests/1004/request_body.json"
	require.NoError(t, svc.SaveDataWithOptions(ctx, ds, key, alreadyCompressed, SaveOptions{Compress: true}))

	onDisk, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(key)))
	require.NoError(t, err)
	require.True(t, isZstdFrame(onDisk), "still a frame")
	require.Equal(t, alreadyCompressed, onDisk, "a frame must be stored as-is, not re-wrapped")

	got, err := svc.LoadData(ctx, ds, key)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// TestLoadDataReadsLegacyUncompressedFile pins backward compatibility: files
// written before compression existed must keep working with no migration.
func TestLoadDataReadsLegacyUncompressedFile(t *testing.T) {
	client, svc, ctx := setupDataStorageTest(t)
	ds, dir := newFSDataStorageForTest(t, client, ctx)

	legacy := []byte(`{"model":"deepseek-v3","legacy":true,"messages":[]}`)
	key := "8/requests/1005/request_body.json"

	full := filepath.Join(dir, filepath.FromSlash(key))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, legacy, 0o644))

	got, err := svc.LoadData(ctx, ds, key)
	require.NoError(t, err)
	require.Equal(t, legacy, got)
}

func TestSaveDataStillWritesPlain(t *testing.T) {
	client, svc, ctx := setupDataStorageTest(t)
	ds, dir := newFSDataStorageForTest(t, client, ctx)

	// SaveData is shared with backups and media; it must stay uncompressed.
	payload := bytes.Repeat([]byte("backup-data"), 1000)
	key := "backups/dump.tar"

	require.NoError(t, svc.SaveData(ctx, ds, key, payload))

	onDisk, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(key)))
	require.NoError(t, err)
	require.False(t, isZstdFrame(onDisk), "SaveData must not compress")

	got, err := svc.LoadData(ctx, ds, key)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestDecompressZstdPassthrough(t *testing.T) {
	t.Run("plain data returned unchanged", func(t *testing.T) {
		in := []byte(`{"plain":true}`)
		out, err := decompressZstd(in)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("empty data returned unchanged", func(t *testing.T) {
		out, err := decompressZstd(nil)
		require.NoError(t, err)
		require.Empty(t, out)
	})

	t.Run("corrupt frame reports an error", func(t *testing.T) {
		corrupt := []byte{0x28, 0xB5, 0x2F, 0xFD, 0x00, 0x01, 0x02, 0x03}
		_, err := decompressZstd(corrupt)
		require.Error(t, err)
	})
}
