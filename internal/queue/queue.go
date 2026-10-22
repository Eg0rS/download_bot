package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

type DownloadRequest struct {
	UserID    int64  `json:"user_id"`
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id"`
	URL       string `json:"url"`
	Quality   string `json:"quality"`
	AudioOnly bool   `json:"audio_only"`
	Priority  int    `json:"priority"`
	CreatedAt int64  `json:"created_at"`
}

type TaskQueue struct {
	client *redis.Client
	key    string
}

func NewTaskQueue(client *redis.Client) *TaskQueue {
	return &TaskQueue{
		client: client,
		key:    "media_downloader:queue",
	}
}

func (q *TaskQueue) Enqueue(req DownloadRequest) error {
	req.CreatedAt = time.Now().Unix()
	req.Priority = 10 // default

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	// Use sorted set with timestamp as score for FIFO
	return q.client.ZAdd(context.Background(), q.key, redis.Z{
		Score:  float64(req.CreatedAt),
		Member: data,
	}).Err()
}

func (q *TaskQueue) Dequeue(count int) ([]DownloadRequest, error) {
	ctx := context.Background()

	// Pop lowest score (oldest) items
	results, err := q.client.ZPopMin(ctx, q.key, int64(count)).Result()
	if err != nil {
		return nil, err
	}

	var tasks []DownloadRequest
	for _, z := range results {
		var req DownloadRequest
		if err := json.Unmarshal([]byte(z.Member.(string)), &req); err != nil {
			logrus.Errorf("Failed to unmarshal queue item: %v", err)
			continue
		}
		tasks = append(tasks, req)
	}

	return tasks, nil
}

func (q *TaskQueue) Requeue(req DownloadRequest) error {
	return q.Enqueue(req)
}

func (q *TaskQueue) Stats() (pending, processing int, err error) {
	ctx := context.Background()
	pending, err = q.client.ZCard(ctx, q.key).Result()
	if err != nil {
		return 0, 0, err
	}

	// Processing tasks tracked in separate key
	processing, err = q.client.ZCard(ctx, "media_downloader:processing").Result()
	return pending, int(processing), err
}

func (q *TaskQueue) MarkProcessing(req DownloadRequest) error {
	data, _ := json.Marshal(req)
	return q.client.ZAdd(context.Background(), "media_downloader:processing", redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: data,
	}).Err()
}

func (q *TaskQueue) MarkDone(req DownloadRequest) error {
	data, _ := json.Marshal(req)
	return q.client.ZRem(context.Background(), "media_downloader:processing", data).Err()
}

func (q *TaskQueue) Len() (int64, error) {
	return q.client.ZCard(context.Background(), q.key).Result()
}

// Worker pool
type WorkerPool struct {
	queue       *TaskQueue
	workerCount int
	handler     func(DownloadRequest) error
	stopCh      chan struct{}
}

func NewWorkerPool(queue *TaskQueue, workerCount int, handler func(DownloadRequest) error) *WorkerPool {
	return &WorkerPool{
		queue:       queue,
		workerCount: workerCount,
		handler:     handler,
		stopCh:      make(chan struct{}),
	}
}

func (wp *WorkerPool) Start() {
	for i := 0; i < wp.workerCount; i++ {
		go wp.worker(i)
	}
	logrus.Infof("Started %d queue workers", wp.workerCount)
}

func (wp *WorkerPool) Stop() {
	close(wp.stopCh)
}

func (wp *WorkerPool) worker(id int) {
	logrus.Debugf("Worker %d started", id)
	for {
		select {
		case <-wp.stopCh:
			logrus.Debugf("Worker %d stopped", id)
			return
		default:
			tasks, err := wp.queue.Dequeue(1)
			if err != nil {
				logrus.Errorf("Worker %d dequeue error: %v", id, err)
				time.Sleep(time.Second)
				continue
			}

			if len(tasks) == 0 {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			task := tasks[0]
			wp.queue.MarkProcessing(task)

			logrus.Infof("Worker %d processing: %s", id, task.URL)
			if err := wp.handler(task); err != nil {
				logrus.Errorf("Worker %d task failed: %v", id, err)
				// Requeue with delay/backoff
				time.Sleep(5 * time.Second)
				wp.queue.Requeue(task)
			} else {
				wp.queue.MarkDone(task)
			}
		}
	}
}

// Helper to create Redis client from config
func NewRedisClient(addr, password string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
		PoolSize: 10,
	})
}

// Parse Redis URL like redis://:password@host:port/db
func ParseRedisURL(url string) (*redis.Options, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return opt, nil
}