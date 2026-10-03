package station

import (
	"context"
	"sync"
	"testing"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

type preparedHarness struct {
	t        *testing.T
	service  *Service
	specimen domain.Specimen
	plan     domain.PrepPlan
}

func setupActivePlan(t *testing.T) *preparedHarness {
	t.Helper()
	service := New(repository.NewMemory())
	ctx := context.Background()

	specimen, err := service.RegisterSpecimen(ctx, contracts.RegisterSpecimenRequest{
		CatalogNumber: "PPS-T-" + t.Name(),
		FieldLabel:    "北坡-07",
		MatrixKind:    "粉砂岩",
	})
	if err != nil {
		t.Fatalf("register specimen: %v", err)
	}
	plan, err := service.CreatePlan(ctx, specimen.ID, contracts.CreatePlanRequest{
		Objective:      "暴露主要关节面",
		Method:         "手工显微剥离",
		TargetExposure: 80,
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if _, err := service.ApprovePlan(ctx, plan.ID, contracts.ApprovalRequest{
		ApprovedBy: "curator-lin",
	}); err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	return &preparedHarness{t: t, service: service, specimen: specimen, plan: plan}
}

func (h *preparedHarness) progress(percent int, stage string) error {
	_, err := h.service.LogProgress(context.Background(), h.plan.ID, contracts.ProgressRequest{
		Stage:      stage,
		Percent:    percent,
		RecordedBy: "preparator-chen",
	})
	return err
}

func (h *preparedHarness) release() error {
	_, err := h.service.ReleaseSpecimen(context.Background(), h.specimen.ID, contracts.ReleaseRequest{
		Purpose:    "比较解剖研究",
		Recipient:  "地层研究组",
		ApprovedBy: "keeper-zhou",
	})
	return err
}

func (h *preparedHarness) refresh(t *testing.T) (domain.Specimen, domain.PrepPlan, []domain.ProgressEntry, bool) {
	t.Helper()
	ctx := context.Background()
	specimen, err := h.service.GetSpecimen(ctx, h.specimen.ID)
	if err != nil {
		t.Fatalf("reload specimen: %v", err)
	}
	snapshot, err := h.service.GetPlan(ctx, h.plan.ID)
	if err != nil {
		t.Fatalf("reload plan: %v", err)
	}
	return specimen, snapshot.Plan, snapshot.Progress, h.hasRelease()
}

func (h *preparedHarness) hasRelease() bool {
	found := false
	_ = h.service.repo.Read(func(m *repository.Mutation) error {
		_, found = m.ReleaseForSpecimen(h.specimen.ID)
		return nil
	})
	return found
}

// 被拒的放行（未稳定标本）不能写入放行记录，也不能阻碍随后的最后进度和正常放行。
func TestRejectedReleaseDoesNotPoisonFinalProgress(t *testing.T) {
	h := setupActivePlan(t)
	if err := h.progress(60, "矩阵剥离"); err != nil {
		t.Fatalf("first progress: %v", err)
	}

	if err := h.release(); err == nil {
		t.Fatal("release on preparing specimen must fail")
	} else if appErr, ok := contracts.AsError(err); !ok ||
		appErr.Code != "specimen_not_releasable" {
		t.Fatalf("expected specimen_not_releasable conflict, got %v", err)
	}

	// 失败的放行不得留下任何放行记录。
	if h.hasRelease() {
		t.Fatal("failed release left a release record")
	}

	// 之后仍可完成最后一条进度。
	if err := h.progress(100, "表面加固"); err != nil {
		t.Fatalf("final progress after rejected release: %v", err)
	}

	specimen, plan, entries, _ := h.refresh(t)
	if specimen.Status != domain.SpecimenStabilized {
		t.Fatalf("expected stabilized, got %s", specimen.Status)
	}
	if plan.Status != domain.PlanCompleted {
		t.Fatalf("expected completed plan, got %s", plan.Status)
	}
	if len(entries) != 2 || entries[1].Percent != 100 {
		t.Fatalf("unexpected progress entries: %+v", entries)
	}

	// 再次放行应正常成功，记录与可借阅状态一起出现。
	if err := h.release(); err != nil {
		t.Fatalf("release after completion: %v", err)
	}
	specimen, plan, entries, released := h.refresh(t)
	if !released {
		t.Fatal("release record missing after successful release")
	}
	if specimen.Status != domain.SpecimenAvailable {
		t.Fatalf("expected available, got %s", specimen.Status)
	}
	if plan.Status != domain.PlanCompleted || entries[len(entries)-1].Percent != 100 {
		t.Fatal("completed progress must remain intact after release")
	}
}

// 字段不合法的放行请求整体回滚，不会让标本被“锁死”。
func TestInvalidReleaseRequestLeavesNoTrace(t *testing.T) {
	h := setupActivePlan(t)
	if err := h.progress(100, "表面加固"); err != nil {
		t.Fatalf("final progress: %v", err)
	}

	_, err := h.service.ReleaseSpecimen(
		context.Background(),
		h.specimen.ID,
		contracts.ReleaseRequest{Purpose: "   ", Recipient: "地层研究组", ApprovedBy: "keeper-zhou"},
	)
	if err == nil {
		t.Fatal("release with empty purpose must fail")
	}
	if appErr, ok := contracts.AsError(err); !ok || appErr.Kind != contracts.KindInvalid {
		t.Fatalf("expected invalid error, got %v", err)
	}

	if h.hasRelease() {
		t.Fatal("invalid release left a release record")
	}
	specimen, err := h.service.GetSpecimen(context.Background(), h.specimen.ID)
	if err != nil {
		t.Fatalf("reload specimen: %v", err)
	}
	if specimen.Status != domain.SpecimenStabilized {
		t.Fatalf("invalid release changed specimen status: %s", specimen.Status)
	}
	if err := h.release(); err != nil {
		t.Fatalf("valid release after invalid attempt: %v", err)
	}
}

// 最后进度与放行并发提交：无论谁先进入事务，最终状态都不能自相矛盾。
func TestConcurrentFinalProgressAndRelease(t *testing.T) {
	for iteration := 0; iteration < 200; iteration++ {
		h := setupActivePlan(t)
		if err := h.progress(50, "矩阵剥离"); err != nil {
			t.Fatalf("initial progress: %v", err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var progressErr, releaseErr error
		go func() {
			defer wg.Done()
			<-start
			progressErr = h.progress(100, "表面加固")
		}()
		go func() {
			defer wg.Done()
			<-start
			releaseErr = h.release()
		}()
		close(start)
		wg.Wait()

		specimen, plan, entries, released := h.refresh(t)

		// 放行记录与可借阅必须同时出现，且方案确实已完成。
		if released {
			if specimen.Status != domain.SpecimenAvailable {
				t.Fatalf("iter %d: release record exists but specimen is %s",
					iteration, specimen.Status)
			}
			if plan.Status != domain.PlanCompleted {
				t.Fatalf("iter %d: released with plan %s", iteration, plan.Status)
			}
			if last := entries[len(entries)-1]; last.Percent != 100 {
				t.Fatalf("iter %d: released but last progress is %d%%",
					iteration, last.Percent)
			}
			if releaseErr != nil {
				t.Fatalf("iter %d: release visible but returned error: %v",
					iteration, releaseErr)
			}
			continue
		}

		// 未放行：进度要么整体提交（completed/stabilized），要么尚未提交，
		// 绝不能出现方案完成而标本仍在制备等撕裂状态。
		if releaseErr == nil {
			t.Fatalf("iter %d: release returned success without record", iteration)
		}
		switch plan.Status {
		case domain.PlanCompleted:
			if specimen.Status != domain.SpecimenStabilized {
				t.Fatalf("iter %d: plan completed but specimen %s",
					iteration, specimen.Status)
			}
			if entries[len(entries)-1].Percent != 100 {
				t.Fatalf("iter %d: completed plan without final 100%% entry", iteration)
			}
			if progressErr != nil {
				t.Fatalf("iter %d: progress committed but returned error: %v",
					iteration, progressErr)
			}
			// 收尾：补一次放行必须成功。
			if err := h.release(); err != nil {
				t.Fatalf("iter %d: follow-up release failed: %v", iteration, err)
			}
		case domain.PlanActive:
			if specimen.Status != domain.SpecimenPreparing {
				t.Fatalf("iter %d: active plan but specimen %s",
					iteration, specimen.Status)
			}
			if progressErr == nil {
				t.Fatalf("iter %d: progress reported success without commit", iteration)
			}
			// 失败的进度可以原样重试并成功。
			if err := h.progress(100, "表面加固"); err != nil {
				t.Fatalf("iter %d: retry final progress: %v", iteration, err)
			}
			if err := h.release(); err != nil {
				t.Fatalf("iter %d: release after retried progress: %v", iteration, err)
			}
		default:
			t.Fatalf("iter %d: unexpected plan status %s", iteration, plan.Status)
		}

		specimen, plan, entries, released = h.refresh(t)
		if !released || specimen.Status != domain.SpecimenAvailable {
			t.Fatalf("iter %d: terminal reconciliation failed: released=%v status=%s",
				iteration, released, specimen.Status)
		}
		if plan.Status != domain.PlanCompleted ||
			entries[len(entries)-1].Percent != 100 {
			t.Fatalf("iter %d: final plan/progress inconsistent", iteration)
		}
	}
}

// 多条放行在标本稳定后并发提交：只有一条成立，其余冲突且不破坏状态。
func TestConcurrentReleasesOnlyOneSucceeds(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		h := setupActivePlan(t)
		if err := h.progress(100, "表面加固"); err != nil {
			t.Fatalf("final progress: %v", err)
		}

		const n = 8
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(n)
		successes := make(chan error, n)
		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				<-start
				successes <- h.release()
			}()
		}
		close(start)
		wg.Wait()
		close(successes)

		ok := 0
		for err := range successes {
			if err == nil {
				ok++
				continue
			}
			if appErr, good := contracts.AsError(err); !good ||
				appErr.Kind != contracts.KindConflict {
				t.Fatalf("iter %d: non-conflict release error: %v", iteration, err)
			}
		}
		if ok != 1 {
			t.Fatalf("iter %d: expected exactly one successful release, got %d",
				iteration, ok)
		}

		specimen, err := h.service.GetSpecimen(context.Background(), h.specimen.ID)
		if err != nil {
			t.Fatalf("reload specimen: %v", err)
		}
		if specimen.Status != domain.SpecimenAvailable {
			t.Fatalf("iter %d: expected available, got %s", iteration, specimen.Status)
		}
		count := 0
		_ = h.service.repo.Read(func(m *repository.Mutation) error {
			_, exists := m.ReleaseForSpecimen(h.specimen.ID)
			if exists {
				count = 1
			}
			return nil
		})
		if count != 1 {
			t.Fatalf("iter %d: expected one release record", iteration)
		}
	}
}
