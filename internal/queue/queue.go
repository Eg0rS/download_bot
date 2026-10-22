package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

type DownloadRequest struct {
	ID        string `json:"request_id"`
	TaskID    uint   `json:"task_id"`
	UserID    int64  `json:"user_id"`
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id"`
	URL       string `json:"url"`
	Quality   string `json:"quality"`
	AudioOnly bool   `json:"audio_only"`
	Priority  int    `json:"priority"`
	CreatedAt int64  `json:"created_at"`
	Attempts  int    `json:"attempts"`
}
type TaskQueue struct {
	client *redis.Client
	key    string
}

func NewTaskQueue(client *redis.Client) *TaskQueue {
	return &TaskQueue{client: client, key: "media_downloader:queue"}
}
func (q *TaskQueue) processingKey() string { return q.key + ":processing" }
func (q *TaskQueue) Enqueue(req DownloadRequest) error {
	if req.ID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return err
		}
		req.ID = hex.EncodeToString(id[:])
	}
	if req.CreatedAt == 0 {
		req.CreatedAt = time.Now().UnixMicro()
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return q.client.ZAdd(ctx, q.key, redis.Z{Score: float64(req.CreatedAt), Member: data}).Err()
}

var claimScript = redis.NewScript(`
local items=redis.call('ZPOPMIN',KEYS[1],ARGV[1])
local result={}
for i=1,#items,2 do
 redis.call('ZADD',KEYS[2],items[i+1],items[i])
 table.insert(result,items[i])
end
return result
`)

func (q *TaskQueue) Dequeue(count int) ([]DownloadRequest, error) {
	if count <= 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := claimScript.Run(ctx, q.client, []string{q.key, q.processingKey()}, count).StringSlice()
	if err != nil {
		return nil, err
	}
	tasks := make([]DownloadRequest, 0, len(raw))
	for _, item := range raw {
		var req DownloadRequest
		if err := json.Unmarshal([]byte(item), &req); err != nil {
			_ = q.client.ZRem(ctx, q.processingKey(), item).Err()
			logrus.Error("Discarded invalid queue item")
			continue
		}
		tasks = append(tasks, req)
	}
	return tasks, nil
}

var retryScript = redis.NewScript(`
redis.call('ZREM',KEYS[2],ARGV[1])
return redis.call('ZADD',KEYS[1],ARGV[3],ARGV[2])
`)

func (q *TaskQueue) Requeue(req DownloadRequest) error {
	return q.requeue(req, true)
}

func (q *TaskQueue) requeue(req DownloadRequest, incrementAttempt bool) error {
	old, _ := json.Marshal(req)
	if incrementAttempt {
		req.Attempts++
	}
	data, _ := json.Marshal(req)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return retryScript.Run(ctx, q.client, []string{q.key, q.processingKey()}, old, data, req.CreatedAt).Err()
}
func (q *TaskQueue) Stats() (pending, processing int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := q.client.ZCard(ctx, q.key).Result()
	if err != nil {
		return 0, 0, err
	}
	active, err := q.client.ZCard(ctx, q.processingKey()).Result()
	return int(p), int(active), err
}
func (q *TaskQueue) MarkProcessing(req DownloadRequest) error { return nil } // Dequeue claims atomically.
func (q *TaskQueue) MarkDone(req DownloadRequest) error {
	data, _ := json.Marshal(req)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return q.client.ZRem(ctx, q.processingKey(), data).Err()
}
func (q *TaskQueue) Len() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return q.client.ZCard(ctx, q.key).Result()
}

// The deployment runs one bot instance. Recover leases left by that instance.
func (q *TaskQueue) Recover() error {
	script := redis.NewScript(`
local items=redis.call('ZRANGE',KEYS[2],0,-1,'WITHSCORES')
for i=1,#items,2 do redis.call('ZADD',KEYS[1],items[i+1],items[i]) end
redis.call('DEL',KEYS[2])
return #items/2
`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return script.Run(ctx, q.client, []string{q.key, q.processingKey()}).Err()
}

type WorkerPool struct {
	queue       *TaskQueue
	workerCount int
	handler     func(DownloadRequest) error
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
}

func NewWorkerPool(q *TaskQueue, n int, handler func(DownloadRequest) error) *WorkerPool {
	return &WorkerPool{queue: q, workerCount: n, handler: handler, stopCh: make(chan struct{})}
}
func (wp *WorkerPool) Start() {
	for i := 0; i < wp.workerCount; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}
}
func (wp *WorkerPool) Stop() { wp.stopOnce.Do(func() { close(wp.stopCh) }); wp.wg.Wait() }
func (wp *WorkerPool) pause(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-wp.stopCh:
		return false
	case <-timer.C:
		return true
	}
}
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()
	for {
		select {
		case <-wp.stopCh:
			return
		default:
		}
		tasks, err := wp.queue.Dequeue(1)
		if err != nil {
			logrus.Errorf("Worker %d dequeue: %v", id, err)
			if !wp.pause(time.Second) {
				return
			}
			continue
		}
		if len(tasks) == 0 {
			if !wp.pause(100 * time.Millisecond) {
				return
			}
			continue
		}
		task := tasks[0]
		err = func() (err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("worker panic: %v", p)
				}
			}()
			return wp.handler(task)
		}()
		if errors.Is(err, context.Canceled) {
			if retryErr := wp.queue.requeue(task, false); retryErr != nil {
				logrus.Errorf("Return interrupted task: %v", retryErr)
			}
			return
		}
		if err != nil && task.Attempts < 2 {
			if retryErr := wp.queue.Requeue(task); retryErr != nil {
				logrus.Errorf("Requeue: %v", retryErr)
			}
			if !wp.pause(time.Second) {
				return
			}
		} else {
			if err != nil {
				logrus.Errorf("Task exhausted retries: %v", err)
			}
			if doneErr := wp.queue.MarkDone(task); doneErr != nil {
				logrus.Errorf("Acknowledge: %v", doneErr)
			}
		}
	}
}
func NewRedisClient(addr, password string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db, PoolSize: 10})
}
func ParseRedisURL(raw string) (*redis.Options, error) { return redis.ParseURL(raw) }
