//go:build integration

package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

// s3Appendable pins the compile-time contract and gives the tests the
// extension's view of the store.
func s3Appendable(t *testing.T, bs *storage.S3BlobStore) storage.AppendableBlobStore {
	t.Helper()
	as, ok := interface{}(bs).(storage.AppendableBlobStore)
	require.True(t, ok, "S3BlobStore must implement AppendableBlobStore")
	return as
}

func s3Content(t *testing.T, bs *storage.S3BlobStore, key string) []byte {
	t.Helper()
	rc, _, err := bs.Get(context.Background(), key)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	return data
}

func TestS3BlobStore_AppendBlob_SmallChunkRoundtrip(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap01aaaa11112222"

	total, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("hello append")))
	require.NoError(t, err)
	assert.EqualValues(t, 12, total)

	// Below the part minimum nothing has been uploaded yet, so the object still
	// has to answer for the staged bytes through AppendedSize.
	size, ok, err := as.AppendedSize(ctx, key)
	require.NoError(t, err)
	require.True(t, ok)
	assert.EqualValues(t, 12, size)

	require.NoError(t, as.FinalizeAppend(ctx, key))
	assert.Equal(t, "hello append", string(s3Content(t, bs, key)))
}

func TestS3BlobStore_AppendBlob_AccumulatesAcrossCalls(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap02bbbb11112222"

	var want string
	for _, chunk := range []string{"alpha", "-beta", "-gamma"} {
		want += chunk
		total, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte(chunk)))
		require.NoError(t, err)
		assert.EqualValues(t, len(want), total)
	}

	require.NoError(t, as.FinalizeAppend(ctx, key))
	assert.Equal(t, want, string(s3Content(t, bs, key)))
}

// The part minimum is where an S3 append differs from a local one: bytes cross
// from the pending tail into a real uploaded part mid-session, and the blob must
// still reassemble byte for byte in the order it was appended.
func TestS3BlobStore_AppendBlob_CrossesPartBoundary(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap03cccc11112222"

	const mib = 1024 * 1024
	first := bytes.Repeat([]byte("A"), 3*mib)
	second := bytes.Repeat([]byte("B"), 3*mib) // pushes the session past 5 MiB
	third := bytes.Repeat([]byte("C"), mib)

	for _, seg := range [][]byte{first, second, third} {
		_, err := as.AppendBlob(ctx, key, bytes.NewReader(seg))
		require.NoError(t, err)
	}
	staged, ok, err := as.AppendedSize(ctx, key)
	require.NoError(t, err)
	require.True(t, ok)
	require.EqualValues(t, 7*mib, staged)

	require.NoError(t, as.FinalizeAppend(ctx, key))

	got := s3Content(t, bs, key)
	require.Len(t, got, 7*mib)
	assert.True(t, bytes.Equal(first, got[:3*mib]), "first segment corrupted")
	assert.True(t, bytes.Equal(second, got[3*mib:6*mib]), "second segment corrupted")
	assert.True(t, bytes.Equal(third, got[6*mib:]), "third segment corrupted")
}

func TestS3BlobStore_TruncateBlob_ShrinksPendingTail(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap04dddd11112222"

	_, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("keep-drop")))
	require.NoError(t, err)
	require.NoError(t, as.TruncateBlob(ctx, key, 4))

	staged, ok, err := as.AppendedSize(ctx, key)
	require.NoError(t, err)
	require.True(t, ok)
	assert.EqualValues(t, 4, staged)

	// The session must stay usable: a rejected chunk costs it nothing.
	total, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("-more")))
	require.NoError(t, err)
	assert.EqualValues(t, 9, total)

	require.NoError(t, as.FinalizeAppend(ctx, key))
	assert.Equal(t, "keep-more", string(s3Content(t, bs, key)))
}

// Rolling back past an uploaded part would mean discarding the whole multipart
// upload, so it fails loudly instead of quietly dropping committed data.
func TestS3BlobStore_TruncateBlob_PastCompletedPart_Errors(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap05eeee11112222"

	_, err := as.AppendBlob(ctx, key, bytes.NewReader(bytes.Repeat([]byte("x"), 6*1024*1024)))
	require.NoError(t, err)

	err = as.TruncateBlob(ctx, key, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already committed")

	require.NoError(t, as.AbortAppend(ctx, key))
}

func TestS3BlobStore_TruncateBlob_PastStaged_Errors(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap06ffff11112222"

	_, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("short")))
	require.NoError(t, err)
	require.Error(t, as.TruncateBlob(ctx, key, 99))
	require.NoError(t, as.AbortAppend(ctx, key))
}

func TestS3BlobStore_FinalizeAppend_NoSessionAndIdempotent(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap07000011112222"

	// No session at all: finalizing must be a no-op, not an error.
	require.NoError(t, as.FinalizeAppend(ctx, key))

	_, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("once")))
	require.NoError(t, err)
	require.NoError(t, as.FinalizeAppend(ctx, key))
	// A client re-sending the finalizing PUT must not corrupt the blob.
	require.NoError(t, as.FinalizeAppend(ctx, key))
	assert.Equal(t, "once", string(s3Content(t, bs, key)))
}

// An abandoned session must really release its multipart upload: parts that
// outlive it stay billable and no listing — so no GC sweep — can see them.
func TestS3BlobStore_AbortAppend_ReleasesSession(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap08111122223333"

	_, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("abandoned")))
	require.NoError(t, err)
	require.NoError(t, as.AbortAppend(ctx, key))
	require.NoError(t, as.AbortAppend(ctx, key), "abort must be idempotent")

	_, ok, err := as.AppendedSize(ctx, key)
	require.NoError(t, err)
	assert.False(t, ok, "an aborted session must leave nothing staged")

	// A later append starts a brand new session rather than resuming the
	// aborted one.
	total, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("fresh")))
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	require.NoError(t, as.FinalizeAppend(ctx, key))
	assert.Equal(t, "fresh", string(s3Content(t, bs, key)))
}

// Appending must extend what the key already holds, exactly as it does on local
// disk — a completed multipart upload replaces the object wholesale, so the
// existing bytes have to be carried into the new upload.
func TestS3BlobStore_AppendBlob_ExtendsExistingObject(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap09222233334444"

	require.NoError(t, bs.Put(ctx, key, bytes.NewReader([]byte("head-")), 5))
	total, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("tail")))
	require.NoError(t, err)
	assert.EqualValues(t, 9, total)

	require.NoError(t, as.FinalizeAppend(ctx, key))
	assert.Equal(t, "head-tail", string(s3Content(t, bs, key)))
}

// The same carry-over for an object too big to become a pending tail: it is
// copied server-side into the upload as its first part.
func TestS3BlobStore_AppendBlob_ExtendsLargeExistingObject(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap10333344445555"

	head := bytes.Repeat([]byte("H"), 6*1024*1024)
	require.NoError(t, bs.Put(ctx, key, bytes.NewReader(head), int64(len(head))))

	total, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("tail")))
	require.NoError(t, err)
	assert.EqualValues(t, len(head)+4, total)

	require.NoError(t, as.FinalizeAppend(ctx, key))
	got := s3Content(t, bs, key)
	require.Len(t, got, len(head)+4)
	assert.True(t, bytes.Equal(head, got[:len(head)]), "carried-over bytes corrupted")
	assert.Equal(t, "tail", string(got[len(head):]))
}

// A session that never received a byte still has to finalize into a real
// zero-byte object: S3 rejects completing an upload with no parts.
func TestS3BlobStore_FinalizeAppend_EmptySession(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap11444455556666"

	_, err := as.AppendBlob(ctx, key, bytes.NewReader(nil))
	require.NoError(t, err)
	require.NoError(t, as.FinalizeAppend(ctx, key))

	size, err := bs.Size(ctx, key)
	require.NoError(t, err)
	assert.Zero(t, size)
}

// Session bookkeeping is not blob content: listing it would inflate usage and
// hand GC an "orphan" whose deletion strands the parts it is the only record of.
func TestS3BlobStore_AppendState_HiddenFromListings(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap12555566667777"

	_, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("staged")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = as.AbortAppend(ctx, key) })

	keys, err := bs.ListKeys(ctx)
	require.NoError(t, err)
	for _, k := range keys {
		assert.NotContains(t, k, ".append-meta", "append bookkeeping must not list as a blob")
	}
	entries, err := bs.ListEntries(ctx)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Key, ".append-meta", "append bookkeeping must not list as a blob")
	}
}

// GC's min_age grace period reads ListEntries' ModTime for a key. An OCI
// chunked upload session starts with a real empty placeholder Put at the
// blob key (mirroring oci.uploadStore.create) — separate from whatever
// AppendBlob's own multipart machinery does later. Every subsequent chunk
// only ever rewrites the .append-meta side-object; without correlating it,
// ListEntries reports the placeholder's frozen session-start ModTime no
// matter how recently a chunk landed, and a long/slow upload can be aborted
// by GC purely because it has been open a while.
func TestS3BlobStore_ListEntries_TracksMostRecentAppendActivity(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap99aaaa88889999"

	require.NoError(t, bs.Put(ctx, key, bytes.NewReader(nil), 0))
	t.Cleanup(func() { _ = as.AbortAppend(ctx, key) })

	entriesAtStart, err := bs.ListEntries(ctx)
	require.NoError(t, err)
	startModTime := findEntry(t, entriesAtStart, key).ModTime

	time.Sleep(1200 * time.Millisecond)

	// A real chunk landing well after session start — the same shape an
	// in-progress, slow OCI upload would produce.
	_, err = as.AppendBlob(ctx, key, bytes.NewReader([]byte("late chunk")))
	require.NoError(t, err)

	entriesAfter, err := bs.ListEntries(ctx)
	require.NoError(t, err)
	afterModTime := findEntry(t, entriesAfter, key).ModTime

	assert.True(t, afterModTime.After(startModTime),
		"ListEntries ModTime should track the most recent AppendBlob activity, not stay pinned to session start (start=%v after=%v)", startModTime, afterModTime)
	assert.GreaterOrEqual(t, afterModTime.Sub(startModTime), 900*time.Millisecond)
}

func findEntry(t *testing.T, entries []storage.BlobEntry, key string) storage.BlobEntry {
	t.Helper()
	for _, e := range entries {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("entry %q not found in ListEntries result", key)
	return storage.BlobEntry{}
}

// Deleting a key mid-session is how GC collects an abandoned upload, and it has
// to take the multipart upload with it.
func TestS3BlobStore_Delete_AbortsInFlightAppend(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap13666677778888"

	_, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("in flight")))
	require.NoError(t, err)
	require.NoError(t, bs.Delete(ctx, key))

	_, ok, err := as.AppendedSize(ctx, key)
	require.NoError(t, err)
	assert.False(t, ok, "delete must leave no append session behind")
}

// failAfterReader hands over data and then fails, modeling a client that aborts
// (or a proxy that drops) partway through a single PATCH.
type failAfterReader struct {
	data []byte
	off  int
	err  error
}

func (r *failAfterReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

// A chunk larger than the part minimum flushes a real, billable part mid-call.
// The session state used to be written only after the whole call succeeded, so
// an error later in that same call — this read failure, or a later part's own
// upload error — returned with the part sitting in S3 and nothing on record
// naming it, not even the upload id that could abort it (#370).
func TestS3BlobStore_AppendBlob_PersistsPartAfterLaterReadFailure(t *testing.T) {
	bs := minioPool(t)
	as := s3Appendable(t, bs)
	ctx := context.Background()
	key := "ap09eeee11112222"

	const mib = 1024 * 1024
	content := bytes.Repeat([]byte("Z"), 6*mib)

	_, err := as.AppendBlob(ctx, key, &failAfterReader{data: content, err: errors.New("client went away")})
	require.Error(t, err)

	staged, ok, err := as.AppendedSize(ctx, key)
	require.NoError(t, err)
	require.True(t, ok, "the append session must survive: a real part is already in the bucket")
	// The 1 MiB tail never reached S3, so it is legitimately dropped — the
	// client re-sends it from the offset reported here at no cost.
	require.EqualValues(t, 5*mib, staged)

	// Resuming from that offset must still produce the original bytes.
	_, err = as.AppendBlob(ctx, key, bytes.NewReader(content[5*mib:]))
	require.NoError(t, err)
	require.NoError(t, as.FinalizeAppend(ctx, key))

	got := s3Content(t, bs, key)
	require.Len(t, got, 6*mib)
	assert.True(t, bytes.Equal(content, got), "resumed blob does not match the original content")
}
