package queue

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskQueue_EnqueueDequeue(t *testing.T) {
	// Requires Redis - skip if not available
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available, skipping integration test")
	}

	// Clean up
	client.Del(ctx, "test_queue", "test_processing")
	defer client.Del(ctx, "test_queue", "test_processing")

	q := &TaskQueue{
		client: client,
		key:    "test_queue",
	}

	req := DownloadRequest{
		UserID:    123,
		ChatID:    456,
		URL:       "https://youtube.com/watch?v=test",
		Quality:   "720p",
		AudioOnly: false,
		Priority:  10,
	}

	// Enqueue
	err := q.Enqueue(req)
	require.NoError(t, err)

	// Dequeue
	tasks, err := q.Dequeue(1)
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	assert.Equal(t, req.UserID, tasks[0].UserID)
	assert.Equal(t, req.URL, tasks[0].URL)
	assert.Equal(t, req.Quality, tasks[0].Quality)
}

func TestTaskQueue_PriorityOrdering(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available")
	}

	client.Del(ctx, "test_priority_queue", "test_priority_processing")
	defer client.Del(ctx, "test_priority_queue", "test_priority_processing")

	q := &TaskQueue{client: client, key: "test_priority_queue"}

	// Enqueue with different priorities (lower score = higher priority in ZPopMin)
	// We use timestamp as score, so first in = first out
	q.Enqueue(DownloadRequest{UserID: 1, URL: "url1", Priority: 5})
	time.Sleep(10 * time.Millisecond)
	q.Enqueue(DownloadRequest{UserID: 2, URL: "url2", Priority: 10})
	time.Sleep(10 * time.Millisecond)
	q.Enqueue(DownloadRequest{UserID: 3, URL: "url3", Priority: 1})

	// Should come out in FIFO order (by timestamp)
	t1, _ := q.Dequeue(1)
	t2, _ := q.Dequeue(1)
	t3, _ := q.Dequeue(1)

	assert.Equal(t, int64(1), t1[0].UserID)
	assert.Equal(t, int64(2), t2[0].UserID)
	assert.Equal(t, int64(3), t3[0].UserID)
}

func TestWorkerPool_Basic(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available")
	}

	client.Del(ctx, "wp_queue", "wp_processing")
	defer client.Del(ctx, "wp_queue", "wp_processing")

	q := &TaskQueue{client: client, key: "wp_queue"}
	processed := make(chan DownloadRequest, 10)

	wp := NewWorkerPool(q, 2, func(req DownloadRequest) error {
		processed <- req
		return nil
	})

	wp.Start()

	// Enqueue some tasks
	q.Enqueue(DownloadRequest{UserID: 1, URL: "url1"})
	q.Enqueue(DownloadRequest{UserID: 2, URL: "url2"})
	q.Enqueue(DownloadRequest{UserID: 3, URL: "url3"})

	// Wait for processing
	time.Sleep(500 * time.Millisecond)
	wp.Stop()
	close(processed)

	// Check all processed
	count := 0
	for range processed {
		count++
	}
	assert.Equal(t, 3, count)
}

func TestClaimRecoveryAndIdenticalRequests(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available")
	}
	q := &TaskQueue{client: client, key: "test_recovery_queue"}
	client.Del(ctx, q.key, q.processingKey())
	defer client.Del(ctx, q.key, q.processingKey())
	req := DownloadRequest{UserID: 1, URL: "https://youtu.be/test", CreatedAt: 1000}
	require.NoError(t, q.Enqueue(req))
	require.NoError(t, q.Enqueue(req))
	items, err := q.Dequeue(2)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.NotEqual(t, items[0].ID, items[1].ID)
	pending, processing, err := q.Stats()
	require.NoError(t, err)
	require.Equal(t, 0, pending)
	require.Equal(t, 2, processing)
	require.NoError(t, q.MarkDone(items[0]))
	require.NoError(t, q.Recover())
	recovered, err := q.Dequeue(1)
	require.NoError(t, err)
	require.Len(t, recovered, 1)
	require.Equal(t, items[1].ID, recovered[0].ID)
	require.NoError(t, q.MarkDone(recovered[0]))
}
