package tasks

import (
	"github.com/spf13/viper"
	"gopds-api/models"
	"gopds-api/utils"
	"io"
	"sync"
)

type TaskType int

const (
	AddBook TaskType = iota
	RemoveBook
	RenumberBooks
	DeleteCollection
)

// CollectionTask struct for collection tasks
type CollectionTask struct {
	Type         TaskType
	CollectionID int64
	Book         models.Book
	Position     int
	NewPositions map[int]int
	Reader       io.Reader
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
		CollectionID: task.CollectionID,
		BasePath:     viper.GetString("app.collections_path"),
	}

	switch task.Type {
	case AddBook:
		err := manager.AddBookToCollection(task.Book, task.Position)
		if err != nil {
			// Логгирование ошибки
		}
	case RemoveBook:
		err := manager.RemoveBookFromCollection(task.Book, task.Position)
		if err != nil {
			// Логгирование ошибки
		}
	case RenumberBooks:
		err := manager.RenumberBooksInCollection(task.NewPositions)
		if err != nil {
			// Логгирование ошибки
		}
	case DeleteCollection:
		err := manager.DeleteCollection()
		if err != nil {
			// Логгирование ошибки
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
