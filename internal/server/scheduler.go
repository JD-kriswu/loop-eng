// Package server provides the scheduler implementation.
package server

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/yourorg/loopany-go/internal/store"
	"github.com/yourorg/loopany-go/pkg/token"
	"github.com/robfig/cron/v3"
)

// Scheduler creates pending runs based on loop schedules.
type Scheduler struct {
	store    *store.Store
	cron     *cron.Cron
	mu       sync.Mutex
	jobs     map[string]cron.EntryID // loopID -> entryID
	jobSpecs map[string]string       // loopID -> cron expression (for change detection)
	stopCh   chan struct{}
	running  bool
}

// NewScheduler creates a scheduler.
func NewScheduler(s *store.Store) *Scheduler {
	return &Scheduler{
		store:    s,
		cron:     cron.New(cron.WithSeconds()),
		jobs:     make(map[string]cron.EntryID),
		jobSpecs: make(map[string]string),
		stopCh:   make(chan struct{}),
	}
}

// Start begins the scheduling loop.
func (s *Scheduler) Start(ctx context.Context) {
	if s.running {
		return
	}
	s.running = true

	// Initial load of all loops
	s.loadLoops(ctx)

	// Start cron engine
	s.cron.Start()

	// Periodic reload and sweep
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.loadLoops(ctx)
				s.sweep(ctx)
			}
		}
	}()

	log.Println("Scheduler started")
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	if !s.running {
		return
	}
	s.running = false
	close(s.stopCh)
	s.cron.Stop()
	log.Println("Scheduler stopped")
}

// loadLoops syncs cron jobs with store.
func (s *Scheduler) loadLoops(ctx context.Context) {
	loops, err := s.store.ListEnabledLoops(ctx)
	if err != nil {
		log.Printf("load loops error: %v", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	log.Printf("[Scheduler] Reloading loops: %d enabled loops in DB, %d scheduled in cron", len(loops), len(s.jobs))

	// Track which loops we've seen
	seen := make(map[string]bool)

	for _, loop := range loops {
		seen[loop.ID] = true

		// Check if loop needs to be rescheduled
		if existingID, exists := s.jobs[loop.ID]; exists {
			// Check if cron expression changed
			oldSpec := s.jobSpecs[loop.ID]
			if oldSpec == loop.Cron {
				// Same cron, no need to reschedule
				continue
			}

			// Cron changed, remove old job
			log.Printf("Loop %s cron changed from '%s' to '%s', rescheduling",
				loop.ID, oldSpec, loop.Cron)
			s.cron.Remove(existingID)
			delete(s.jobs, loop.ID)
			delete(s.jobSpecs, loop.ID)
		}

		// Schedule the loop (new or updated)
		if err := s.scheduleLoop(ctx, loop); err != nil {
			log.Printf("schedule loop %s error: %v", loop.ID, err)
		}
	}

	// Remove jobs for disabled loops
	for loopID, entryID := range s.jobs {
		if !seen[loopID] {
			s.cron.Remove(entryID)
			delete(s.jobs, loopID)
			delete(s.jobSpecs, loopID)
			log.Printf("Removed disabled loop %s", loopID)
		}
	}
}

// scheduleLoop adds a loop to the cron schedule.
func (s *Scheduler) scheduleLoop(ctx context.Context, loop *store.Loop) error {
	if loop.Cron == "" {
		return nil
	}

	// Parse timezone
	if loop.Timezone != "" {
		if _, err := time.LoadLocation(loop.Timezone); err != nil {
			log.Printf("Warning: invalid timezone %s for loop %s: %v", loop.Timezone, loop.ID, err)
		}
	}

	schedule, err := cron.ParseStandard(loop.Cron)
	if err != nil {
		return err
	}

	// Create job
	job := cron.FuncJob(func() {
		s.fire(ctx, loop)
	})

	// Schedule with location
	entryID := s.cron.Schedule(schedule, job)
	s.jobs[loop.ID] = entryID
	s.jobSpecs[loop.ID] = loop.Cron // Save cron spec for change detection

	log.Printf("Scheduled loop %s: %s (%s)", loop.ID, loop.Name, loop.Cron)
	return nil
}

// fire creates a pending run for a loop.
func (s *Scheduler) fire(ctx context.Context, loop *store.Loop) {
	// Check if loop is still enabled
	current, err := s.store.GetLoop(ctx, loop.ID)
	if err != nil || !current.Enabled {
		return
	}

	// Check if there's already a pending run for this loop
	runs, _ := s.store.ListRunsByLoop(ctx, loop.ID, 1)
	if len(runs) > 0 && runs[0].Status == "pending" {
		log.Printf("Loop %s already has pending run, skipping", loop.ID)
		return
	}

	// Create new pending run
	run := &store.Run{
		ID:        token.GenerateID(),
		LoopID:    loop.ID,
		MachineID: loop.MachineID,
		Role:      "exec",
		Status:    "pending",
		StartedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	if err := s.store.CreatePendingRun(ctx, run); err != nil {
		log.Printf("create run error for loop %s: %v", loop.ID, err)
		return
	}

	log.Printf("Created run %s for loop %s", run.ID, loop.ID)
}

// sweep cleans up stale machines and stuck runs.
func (s *Scheduler) sweep(ctx context.Context) {
	// Mark machines offline if not seen in 30s
	// Reclaim runs that didn't report

	// Implementation would query machines where last_seen < now - 30s
	// and runs where status = 'running' and started_at < now - 20min
	log.Println("Sweeping stale machines and stuck runs")
}

// ScheduleLoop manually schedules or reschedules a loop.
func (s *Scheduler) ScheduleLoopNow(loopID string) error {
	ctx := context.Background()
	loop, err := s.store.GetLoop(ctx, loopID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove existing job if any
	if entryID, exists := s.jobs[loopID]; exists {
		s.cron.Remove(entryID)
		delete(s.jobs, loopID)
	}

	// Add new job
	if loop.Cron != "" {
		return s.scheduleLoop(ctx, loop)
	}

	return nil
}