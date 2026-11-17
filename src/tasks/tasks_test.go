package tasks

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

const testOwner = "browser-1234567890123456"

func awaitTask(t *testing.T, m *Manager, id string) Task {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		snapshot, ok := m.Get(testOwner, id)
		if ok && terminal(snapshot.State) {
			return snapshot
		}
		select {
		case <-timer.C:
			t.Fatal("task did not finish")
		case <-tick.C:
		}
	}
}

func TestTaskIsolationSnapshotsAndBoundedLog(t *testing.T) {
	m := New(1)
	task := m.Start(testOwner, "fixture", "Fixture", func(ctx context.Context, r *Reporter) (any, error) {
		for range 300 {
			r.Log("safe message")
		}
		r.Progress(math.NaN())
		r.Progress(75)
		return map[string]string{"artifactId": "fixture-id"}, nil
	})
	result := awaitTask(t, m, task.ID)
	if result.State != "succeeded" || result.Progress != 100 || len(result.Logs) != 256 {
		t.Fatalf("unexpected task state: %+v", result)
	}
	if _, ok := m.Get("another-browser-123456", task.ID); ok || len(m.List("another-browser-123456")) != 0 {
		t.Fatal("another browser could inspect a task")
	}
	result.Logs[0] = "mutated"
	again, _ := m.Get(testOwner, task.ID)
	if strings.Contains(again.Logs[0], "mutated") {
		t.Fatal("snapshot logs alias mutable task state")
	}
}

func TestQueuedCancellationNeverRunsOperation(t *testing.T) {
	m := New(1)
	started := make(chan struct{})
	release := make(chan struct{})
	ran := make(chan struct{}, 1)
	first := m.Start(testOwner, "fixture", "First", func(ctx context.Context, r *Reporter) (any, error) { close(started); <-release; return nil, nil })
	<-started
	second := m.Start(testOwner, "fixture", "Second", func(ctx context.Context, r *Reporter) (any, error) { ran <- struct{}{}; return "secret-result", nil })
	if m.Cancel("wrong-browser-123456", second.ID) {
		t.Fatal("another browser cancelled the task")
	}
	if !m.Cancel(testOwner, second.ID) {
		t.Fatal("owner cancellation failed")
	}
	result := awaitTask(t, m, second.ID)
	if result.State != "cancelled" || result.Result != nil {
		t.Fatal("cancelled task retained a result")
	}
	close(release)
	awaitTask(t, m, first.ID)
	select {
	case <-ran:
		t.Fatal("cancelled queued operation ran")
	default:
	}
}

func TestFailureDoesNotExposeRawCredentialError(t *testing.T) {
	m := New(1)
	failed := m.Start(testOwner, "fixture", "Failure", func(context.Context, *Reporter) (any, error) {
		return nil, errors.New("private-password-should-not-appear")
	})
	result := awaitTask(t, m, failed.ID)
	if strings.Contains(result.Error, "private-password") || result.State != "failed" {
		t.Fatal("raw provider error was exposed")
	}
	public := m.Start(testOwner, "fixture", "Public", func(context.Context, *Reporter) (any, error) { return nil, PublicError("No matching enrollment model") })
	if result := awaitTask(t, m, public.ID); result.Error != "No matching enrollment model" {
		t.Fatal("safe actionable error was lost")
	}
}
