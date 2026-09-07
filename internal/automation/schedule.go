// Package automation starts agents on a clock or on an outside event.
//
// Both halves share one hard requirement: a run that nobody is watching must
// either happen or leave a visible reason why it did not. A scheduled task that
// dies silently at 2am is worse than no scheduled task, because you plan around
// it. So every path here records its outcome on the schedule or the trigger,
// and a missed window is caught up rather than skipped.
package automation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/panjitito/go-ai-team/internal/store"
)

// Launcher is what actually starts an agent. Supplied by the caller so this
// package does not depend on the session manager.
type Launcher interface {
	// LaunchTask starts an agent in a project with a first prompt, and returns
	// the session id. Origin is a short label used in the terminal banner.
	LaunchTask(projectID, agentID, prompt, origin string) (sessionID string, err error)
}

// Notifier reports what happened, so the UI can surface it.
type Notifier interface {
	Notify(kind, message string, payload any)
}

// Scheduler fires schedules when they come due.
type Scheduler struct {
	st  *store.Store
	l   Launcher
	n   Notifier
	mu  sync.Mutex
	now func() time.Time
}

// NewScheduler builds a Scheduler.
func NewScheduler(st *store.Store, l Launcher, n Notifier) *Scheduler {
	return &Scheduler{st: st, l: l, n: n, now: time.Now}
}

// Start begins the tick loop and catches up anything already overdue.
func (s *Scheduler) Start(ctx context.Context) {
	s.catchUp()
	go func() {
		// Half a minute is fine: the finest cadence offered is one minute, and
		// a schedule firing up to 30 seconds late is not a defect.
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.tick()
			}
		}
	}()
}

// catchUp runs anything whose window was missed while the app was closed, which
// is the difference between a schedule you can rely on and one that silently
// skips every night you shut the laptop.
func (s *Scheduler) catchUp() {
	for _, sc := range s.st.Schedules() {
		if !sc.Enabled {
			continue
		}
		if sc.NextRun.IsZero() {
			// Never scheduled: set the first window without running now.
			next := NextRun(sc, s.now())
			_, _ = s.st.UpdateSchedule(sc.ID, func(x *store.Schedule) { x.NextRun = next })
			continue
		}
		if s.now().After(sc.NextRun) {
			s.fire(sc, "missed window, caught up on launch")
		}
	}
}

func (s *Scheduler) tick() {
	now := s.now()
	for _, sc := range s.st.Schedules() {
		if !sc.Enabled {
			continue
		}
		if sc.NextRun.IsZero() {
			next := NextRun(sc, now)
			_, _ = s.st.UpdateSchedule(sc.ID, func(x *store.Schedule) { x.NextRun = next })
			continue
		}
		if now.After(sc.NextRun) {
			s.fire(sc, "on schedule")
		}
	}
}

// fire launches one schedule and advances its window. The window advances even
// when the launch fails, so a persistently broken schedule does not retry every
// thirty seconds forever.
func (s *Scheduler) fire(sc *store.Schedule, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Re-read under the lock: a tick and a catch-up could both have selected
	// this schedule, and it must only run once.
	cur, err := s.st.Schedule(sc.ID)
	if err != nil || !cur.Enabled {
		return
	}
	if !cur.NextRun.IsZero() && s.now().Before(cur.NextRun) {
		return
	}

	next := NextRun(cur, s.now())
	sid, launchErr := s.l.LaunchTask(cur.ProjectID, cur.AgentID, cur.Prompt,
		fmt.Sprintf("scheduled task %q (%s)", cur.Name, why))

	_, _ = s.st.UpdateSchedule(cur.ID, func(x *store.Schedule) {
		x.LastRun = s.now()
		x.NextRun = next
		x.RunCount++
		if launchErr != nil {
			x.LastErr = launchErr.Error()
		} else {
			x.LastErr = ""
		}
	})

	if launchErr != nil {
		s.n.Notify("schedule.failed",
			fmt.Sprintf("Scheduled task %q could not start: %v", cur.Name, launchErr), nil)
		return
	}
	s.n.Notify("schedule.ran",
		fmt.Sprintf("Scheduled task %q started", cur.Name),
		map[string]string{"scheduleId": cur.ID, "sessionId": sid})
}

// RunNow fires a schedule immediately, without disturbing its cadence.
func (s *Scheduler) RunNow(id string) (string, error) {
	sc, err := s.st.Schedule(id)
	if err != nil {
		return "", err
	}
	sid, err := s.l.LaunchTask(sc.ProjectID, sc.AgentID, sc.Prompt,
		fmt.Sprintf("scheduled task %q (run now)", sc.Name))
	_, _ = s.st.UpdateSchedule(id, func(x *store.Schedule) {
		x.LastRun = time.Now()
		x.RunCount++
		if err != nil {
			x.LastErr = err.Error()
		} else {
			x.LastErr = ""
		}
	})
	return sid, err
}

// NextRun computes the next firing time strictly after from.
//
// Written out rather than pulling in a cron library because the cadences
// offered are a fixed, small set chosen to be explainable in the UI — "every 15
// minutes", "Mondays at 09:00" — and a cron expression is exactly the thing
// this feature exists to avoid asking for.
func NextRun(sc *store.Schedule, from time.Time) time.Time {
	from = from.Truncate(time.Second)
	loc := from.Location()

	switch sc.Every {
	case "minutes":
		n := sc.N
		if n < 1 {
			n = 15
		}
		return from.Add(time.Duration(n) * time.Minute)

	case "hourly":
		m := clampInt(sc.Minute, 0, 59)
		t := time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), m, 0, 0, loc)
		if !t.After(from) {
			t = t.Add(time.Hour)
		}
		return t

	case "daily":
		h := clampInt(sc.Hour, 0, 23)
		m := clampInt(sc.Minute, 0, 59)
		t := time.Date(from.Year(), from.Month(), from.Day(), h, m, 0, 0, loc)
		if !t.After(from) {
			t = t.AddDate(0, 0, 1)
		}
		return t

	case "weekly":
		h := clampInt(sc.Hour, 0, 23)
		m := clampInt(sc.Minute, 0, 59)
		want := time.Weekday(clampInt(sc.Wday, 0, 6))
		t := time.Date(from.Year(), from.Month(), from.Day(), h, m, 0, 0, loc)
		// Step forward to the wanted weekday, then one more week if that
		// moment has already passed today.
		delta := (int(want) - int(t.Weekday()) + 7) % 7
		t = t.AddDate(0, 0, delta)
		if !t.After(from) {
			t = t.AddDate(0, 0, 7)
		}
		return t

	case "monthly":
		h := clampInt(sc.Hour, 0, 23)
		m := clampInt(sc.Minute, 0, 59)
		day := clampInt(sc.Mday, 1, 31)
		t := monthDay(from.Year(), from.Month(), day, h, m, loc)
		if !t.After(from) {
			y, mo := from.Year(), from.Month()+1
			if mo > 12 {
				y, mo = y+1, 1
			}
			t = monthDay(y, mo, day, h, m, loc)
		}
		return t
	}
	// An unknown cadence must not become a hot loop.
	return from.Add(time.Hour)
}

// monthDay builds a date, clamping the day to the length of the month so "the
// 31st" still fires in February instead of silently rolling into March.
func monthDay(y int, mo time.Month, day, h, m int, loc *time.Location) time.Time {
	last := time.Date(y, mo+1, 0, 0, 0, 0, 0, loc).Day()
	if day > last {
		day = last
	}
	return time.Date(y, mo, day, h, m, 0, 0, loc)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Describe renders a cadence in words, for the UI.
func Describe(sc *store.Schedule) string {
	switch sc.Every {
	case "minutes":
		n := sc.N
		if n < 1 {
			n = 15
		}
		if n == 1 {
			return "every minute"
		}
		return fmt.Sprintf("every %d minutes", n)
	case "hourly":
		return fmt.Sprintf("every hour at :%02d", clampInt(sc.Minute, 0, 59))
	case "daily":
		return fmt.Sprintf("every day at %02d:%02d", clampInt(sc.Hour, 0, 23), clampInt(sc.Minute, 0, 59))
	case "weekly":
		return fmt.Sprintf("%ss at %02d:%02d",
			time.Weekday(clampInt(sc.Wday, 0, 6)), clampInt(sc.Hour, 0, 23), clampInt(sc.Minute, 0, 59))
	case "monthly":
		return fmt.Sprintf("day %d each month at %02d:%02d",
			clampInt(sc.Mday, 1, 31), clampInt(sc.Hour, 0, 23), clampInt(sc.Minute, 0, 59))
	}
	return sc.Every
}
