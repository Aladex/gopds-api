package tasks

import (
	"github.com/spf13/viper"
	"gopds-api/models"
	"gopds-api/utils"
	"sync"
)

type TaskType int

const (
	UpdateCollection TaskType = iota
	DeleteCollection
)

// CollectionTask struct for collection tasks
type CollectionTask struct {
	Type         TaskType
	CollectionID int64
	UpdatedBooks []models.Book
}

// TaskManager struct for managing tasks
type TaskManager struct {
	queues map[int64]chan CollectionTask
	mu     sync.Mutex
	wg     sync.WaitGroup
}

// NewTaskManager creates a new TaskManager
func NewTaskManager() *TaskManager {
	return &TaskManager{
		queues: make(map[int64]chan CollectionTask),
	}
}

// EnqueueTask adds a task to the queue
func (tm *TaskManager) EnqueueTask(task CollectionTask) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, exists := tm.queues[task.CollectionID]; !exists {
		tm.queues[task.CollectionID] = make(chan CollectionTask, 100)
		tm.startWorker(task.CollectionID)
	}

	tm.queues[task.CollectionID] <- task
}

// startWorker starts a worker for the given collection
func (tm *TaskManager) startWorker(collectionID int64) {
	tm.wg.Add(1)
	go func() {
		defer tm.wg.Done()
		for task := range tm.queues[collectionID] {
			tm.processTask(task)
		}
	}()
}

// processTask processes the task
func (tm *TaskManager) processTask(task CollectionTask) {
	manager := utils.CollectionManager{
		BasePath: viper.GetString("app.collections_path"),
	}

	switch task.Type {
	case UpdateCollection:
		err := manager.UpdateBookCollection(task.CollectionID, task.UpdatedBooks)
		if err != nil {
			// Error logging
		}

	case DeleteCollection:
		err := manager.DeleteCollection(task.CollectionID)
		if err != nil {
			// Error logging
		}
	}
}

// StopAllWorkers stops all workers
func (tm *TaskManager) StopAllWorkers() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	for _, queue := range tm.queues {
		close(queue)
	}
	tm.wg.Wait()
}
