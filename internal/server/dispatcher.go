// Package server contains the dispatcher implementation.
package server

import (
	"context"
	"sync"
	"time"

	"github.com/yourorg/loopany-go/internal/protocol"
	"github.com/yourorg/loopany-go/internal/server/prompts"
	"github.com/yourorg/loopany-go/internal/store"
)

// Dispatcher manages run claims and deliveries.
type Dispatcher struct {
	store    *store.Store
	mu       sync.Mutex
	pending  map[string][]*store.Run // machineID -> pending runs
	waiters  map[string]chan struct{} // machineID -> wait channel
}

// NewDispatcher creates a dispatcher.
func NewDispatcher(s *store.Store) *Dispatcher {
	return &Dispatcher{
		store:   s,
		pending: make(map[string][]*store.Run),
		waiters: make(map[string]chan struct{}),
	}
}

// ClaimPending returns runs for a machine to execute.
// If wait=true and no pending runs, blocks for up to 20s.
func (d *Dispatcher) ClaimPending(machineID string, wait bool) []protocol.Delivery {
	d.mu.Lock()
	
	// Check in-memory pending first
	if runs, ok := d.pending[machineID]; ok && len(runs) > 0 {
		claimed := d.buildDeliveries(runs)
		delete(d.pending, machineID)
		d.mu.Unlock()
		return claimed
	}
	
	// Query store for pending runs
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	runs, err := d.store.ListPendingRuns(ctx, machineID)
	if err == nil && len(runs) > 0 {
		claimed := d.buildDeliveries(runs)
		d.mu.Unlock()
		return claimed
	}
	
	if !wait {
		d.mu.Unlock()
		return nil
	}
	
	// Wait for work
	waitCh := make(chan struct{}, 1)
	d.waiters[machineID] = waitCh
	d.mu.Unlock()
	
	select {
	case <-waitCh:
		d.mu.Lock()
		defer d.mu.Unlock()
		if runs, ok := d.pending[machineID]; ok {
			claimed := d.buildDeliveries(runs)
			delete(d.pending, machineID)
			return claimed
		}
	case <-time.After(20 * time.Second):
		// Timeout - return empty
	}
	
	return nil
}

// NotifyPending wakes up a waiting machine.
func (d *Dispatcher) NotifyPending(machineID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if ch, ok := d.waiters[machineID]; ok {
		select {
		case ch <- struct{}{}:
		default:
		}
		delete(d.waiters, machineID)
	}
}

// AddPending queues a run for a machine.
func (d *Dispatcher) AddPending(machineID string, run *store.Run) {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	d.pending[machineID] = append(d.pending[machineID], run)
	
	// Wake up waiter if any
	if ch, ok := d.waiters[machineID]; ok {
		select {
		case ch <- struct{}{}:
		default:
		}
		delete(d.waiters, machineID)
	}
}

func (d *Dispatcher) buildDeliveries(runs []*store.Run) []protocol.Delivery {
	deliveries := make([]protocol.Delivery, 0, len(runs))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, run := range runs {
		// Get loop info
		loop, err := d.store.GetLoop(ctx, run.LoopID)
		if err != nil {
			continue
		}

		// Build exec-core task prompt
		taskFile := loop.TaskFile
		if taskFile == "" {
			taskFile = "(none — this loop has no task file yet)"
		}
		
		goalLine := ""
		if loop.Goal != "" {
			goalLine = "Goal (finish line): " + loop.Goal
		}
		
		// Build the full task from exec-core template
		task := prompts.BuildExecTask(prompts.ExecCoreData{
			Name:      loop.Name,
			TaskFile:  taskFile,
			GoalLine:  goalLine,
			StateLine: "loopany report --status new --message \"<message>\"", // simplified
		})

		// canFinish: only exec runs on closed loops (goal != null)
		canFinish := run.Role == protocol.RoleExec && loop.Goal != ""

		delivery := protocol.Delivery{
			RunID:    run.ID,
			RunToken: generateRunToken(run.ID),
			Role:     run.Role,
			Loop: protocol.LoopInfo{
				ID:           loop.ID,
				Name:         loop.Name,
				Workdir:      loop.Workdir,
				TaskFile:     loop.TaskFile,
				Workflow:     loop.Workflow,
				Model:        loop.Model,
				Agent:        loop.Agent,
				AllowControl: loop.AllowControl,
				Goal:         loop.Goal,
			},
			Task:      task,
			CanFinish: canFinish,
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries
}

func generateRunToken(runID string) string {
	// Would generate signed token
	return runID + ":run:token"
}