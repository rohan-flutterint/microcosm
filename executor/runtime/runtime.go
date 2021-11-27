package runtime

import (
	"context"
	"sync"
)

type queue struct {
	sync.Mutex
	tasks []*taskContainer
}

func (q *queue) pop() *taskContainer {
	q.Lock()
	defer q.Unlock()
	if len(q.tasks) == 0 {
		return nil
	}
	task := q.tasks[0]
	q.tasks = q.tasks[1:]
	return task
}

func (q *queue) push(t *taskContainer) {
	q.Lock()
	defer q.Unlock()
	q.tasks = append(q.tasks, t)
}

type Runtime struct {
	q   queue
}

func (s *Runtime) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		t := s.q.pop()
		if t == nil {
			// idle
			continue
		}
		status := t.Poll()
		if status == Blocked {
			if t.tryBlock() {
				continue
			}
		}
		t.setRunnable()
		s.q.push(t)
	}
}

func NewRuntime() *Runtime {
	s := &Runtime{}
	return s
}