package storage

import (
	"context"
	"testing"
	"time"

	"example.com/spillway-gate-coordinator/internal/domain"
)

func TestCommandsResolveToTheirOwnTargetsWithoutStaleDeltas(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()

	gate, err := domain.NewGate("gate-1", "一号泄洪闸", 5, 0.5, now)
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	if err := repository.CreateGate(ctx, gate); err != nil {
		t.Fatalf("CreateGate() error = %v", err)
	}

	// Both commands are waiting for review before either command changes the gate.
	first := mustNewCommand(t, "cmd-1", "gate-1", 1.5, 0.5, 1, now)
	second := mustNewCommand(t, "cmd-2", "gate-1", 2.5, 0.5, 1, now)
	if err := repository.CreateCommand(ctx, first); err != nil {
		t.Fatalf("CreateCommand(first) error = %v", err)
	}
	if err := repository.CreateCommand(ctx, second); err != nil {
		t.Fatalf("CreateCommand(second) error = %v", err)
	}

	first, gate, err = repository.ReviewCommand(ctx, first.ID, clearDecision(now), "token-1", now)
	if err != nil {
		t.Fatalf("ReviewCommand(first) error = %v", err)
	}
	first, gate, err = repository.ExecuteCommand(ctx, first.ID, "token-1", "operator-a", false, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ExecuteCommand(first) error = %v", err)
	}
	if gate.Aperture != 1.5 {
		t.Fatalf("first gate aperture = %v, want 1.5", gate.Aperture)
	}

	second, gate, err = repository.ReviewCommand(ctx, second.ID, clearDecision(now.Add(2*time.Minute)), "token-2", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ReviewCommand(second) error = %v", err)
	}
	second, gate, err = repository.ExecuteCommand(ctx, second.ID, "token-2", "operator-b", false, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("ExecuteCommand(second) error = %v", err)
	}
	if gate.Aperture != 2.5 {
		t.Fatalf("second gate aperture = %v, want 2.5; stale delta would move it to 3.5", gate.Aperture)
	}

	storedFirst, err := repository.Command(ctx, first.ID)
	if err != nil {
		t.Fatalf("Command(first) error = %v", err)
	}
	if storedFirst.Status != domain.CommandCompleted || storedFirst.TargetAperture != 1.5 || storedFirst.StartingAperture != 0.5 {
		t.Fatalf("completed history was rewritten: %+v", storedFirst)
	}
	if _, _, err := repository.ExecuteCommand(ctx, second.ID, "token-2", "operator-b", false, now.Add(4*time.Minute)); err == nil {
		t.Fatal("reusing a completed command token succeeded")
	}
}

func TestCommandCreatedAfterSettlementUsesCurrentAperture(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()

	gate, err := domain.NewGate("gate-2", "二号泄洪闸", 5, 0.5, now)
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	if err := repository.CreateGate(ctx, gate); err != nil {
		t.Fatalf("CreateGate() error = %v", err)
	}

	first := mustNewCommand(t, "cmd-1", "gate-2", 1.5, 0.5, 1, now)
	if err := repository.CreateCommand(ctx, first); err != nil {
		t.Fatalf("CreateCommand(first) error = %v", err)
	}
	if _, _, err := repository.ReviewCommand(ctx, first.ID, clearDecision(now), "token-1", now); err != nil {
		t.Fatalf("ReviewCommand(first) error = %v", err)
	}
	if _, _, err := repository.ExecuteCommand(ctx, first.ID, "token-1", "operator-a", false, now.Add(time.Minute)); err != nil {
		t.Fatalf("ExecuteCommand(first) error = %v", err)
	}

	second := mustNewCommand(t, "cmd-2", "gate-2", 2.5, 0.5, 1, now.Add(2*time.Minute))
	err = repository.CreateCommand(ctx, second)
	if err == nil {
		t.Fatal("CreateCommand() with a stale pre-store aperture succeeded")
	}
	if domain.ErrorCodeOf(err) != "gate_aperture_changed" {
		t.Fatalf("CreateCommand() error code = %q, want gate_aperture_changed", domain.ErrorCodeOf(err))
	}

	second = mustNewCommand(t, "cmd-2", "gate-2", 2.5, 1.5, 2, now.Add(2*time.Minute))
	if err := repository.CreateCommand(ctx, second); err != nil {
		t.Fatalf("CreateCommand(second) error = %v", err)
	}
	storedSecond, err := repository.Command(ctx, second.ID)
	if err != nil {
		t.Fatalf("Command(second) error = %v", err)
	}
	if storedSecond.StartingAperture != 1.5 || storedSecond.Movement.BaseAperture != 1.5 || storedSecond.Movement.BaseRevision != 2 {
		t.Fatalf("new command did not use current gate basis: %+v", storedSecond.Movement)
	}
}

func TestAbortedCommandHistoryIsNotRewritten(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()

	gate, err := domain.NewGate("gate-3", "三号泄洪闸", 5, 0.5, now)
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	if err := repository.CreateGate(ctx, gate); err != nil {
		t.Fatalf("CreateGate() error = %v", err)
	}
	command := mustNewCommand(t, "cmd-1", "gate-3", 1.5, 0.5, 1, now)
	if err := repository.CreateCommand(ctx, command); err != nil {
		t.Fatalf("CreateCommand() error = %v", err)
	}
	if _, _, err := repository.ReviewCommand(ctx, command.ID, clearDecision(now), "token-1", now); err != nil {
		t.Fatalf("ReviewCommand() error = %v", err)
	}
	completed, settledGate, err := repository.ExecuteCommand(ctx, command.ID, "token-1", "operator-a", true, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ExecuteCommand(fault) error = %v", err)
	}
	if completed.Status != domain.CommandAborted {
		t.Fatalf("faulted command status = %s, want aborted", completed.Status)
	}
	if settledGate.Status != domain.GateFaulted || settledGate.Aperture != 1.5 {
		t.Fatalf("faulted gate = status %s aperture %v, want faulted/1.5", settledGate.Status, settledGate.Aperture)
	}
	if completed.StartingAperture != 0.5 || completed.TargetAperture != 1.5 {
		t.Fatalf("aborted history was rewritten: %+v", completed)
	}
}

func mustNewCommand(
	t *testing.T,
	id string,
	gateID string,
	target float64,
	starting float64,
	revision int64,
	now time.Time,
) domain.GateCommand {
	t.Helper()
	command, err := domain.NewGateCommand(id, gateID, target, starting, 5, revision, "测试调整", "dispatcher", now)
	if err != nil {
		t.Fatalf("NewGateCommand(%s) error = %v", id, err)
	}
	return command
}

func clearDecision(now time.Time) domain.InterlockDecision {
	return domain.InterlockDecision{
		Verdict:    domain.InterlockClear,
		Verifier:   "safety",
		Note:       "clear",
		ReviewedAt: now,
	}
}
