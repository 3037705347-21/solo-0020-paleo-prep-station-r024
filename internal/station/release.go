package station

import (
	"context"
	"runtime"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) beginReleaseTerminal(
	ctx context.Context,
	specimenID string,
) (domain.TerminalTransition, error) {
	if err := ctx.Err(); err != nil {
		return domain.TerminalTransition{}, err
	}

	var transition domain.TerminalTransition
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
			return notFoundSpecimen(specimenID)
		}
		planID := ""
		if existing, ok := m.TransitionForSpecimen(specimenID); ok {
			if existing.Kind != domain.TerminalProgress ||
				!specimen.IsStable() ||
				m.HasOpenPlan(specimenID) {
				return contracts.Conflict(
					"terminal_transition_pending",
					"该标本已有其他处理终态正在确认",
				)
			}
			planID = existing.PlanID
		}
		value, err := domain.NewReleaseTransition(
			s.nextID("transition"),
			specimen,
			planID,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.PutTransition(value)
		transition = value
		return nil
	})
	return transition, err
}

func (s *Service) recordReleaseTerminal(
	ctx context.Context,
	transition domain.TerminalTransition,
	request contracts.ReleaseRequest,
) (domain.ReleaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReleaseRecord{}, err
	}

	var created domain.ReleaseRecord
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		current, ok := m.TransitionForSpecimen(transition.SpecimenID)
		if !ok {
			return contracts.Conflict(
				"terminal_transition_missing",
				"处理终态已经失效",
			)
		}
		if current.ID != transition.ID ||
			current.Kind != domain.TerminalRelease ||
			current.Phase != domain.TerminalPrepared {
			return contracts.Conflict(
				"terminal_transition_changed",
				"处理终态已被其他请求改变",
			)
		}
		specimen, ok := m.GetSpecimen(transition.SpecimenID)
		if !ok {
			return notFoundSpecimen(transition.SpecimenID)
		}
		if _, exists := m.ReleaseForSpecimen(specimen.ID); exists {
			return contracts.Conflict(
				"release_exists",
				"该标本已经存在放行记录",
			)
		}
		record, err := domain.NewReleaseRecordFromTransition(
			s.nextID("release"),
			specimen,
			current,
			request.Purpose,
			request.Recipient,
			request.ApprovedBy,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := current.MarkRecorded(s.now()); err != nil {
			return err
		}
		m.PutRelease(record)
		m.PutTransition(current)
		created = record
		return nil
	})
	return created, err
}

func (s *Service) finalizeReleaseTerminal(
	ctx context.Context,
	transition domain.TerminalTransition,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.repo.Mutate(func(m *repository.Mutation) error {
		current, ok := m.TransitionForSpecimen(transition.SpecimenID)
		if !ok {
			return contracts.Conflict(
				"terminal_transition_missing",
				"处理终态已经失效",
			)
		}
		if current.ID != transition.ID ||
			current.Kind != domain.TerminalRelease ||
			current.Phase != domain.TerminalRecorded {
			return contracts.Conflict(
				"terminal_transition_changed",
				"处理终态已被其他请求改变",
			)
		}
		specimen, ok := m.GetSpecimen(transition.SpecimenID)
		if !ok {
			return notFoundSpecimen(transition.SpecimenID)
		}
		if !specimen.IsStable() {
			return contracts.Conflict(
				"specimen_not_releasable",
				"只有已稳定标本可以办理放行",
			)
		}
		if err := specimen.MarkAvailable(s.now()); err != nil {
			return err
		}
		m.PutSpecimen(specimen)
		m.DeleteTransition(transition.SpecimenID)
		return nil
	})
}

func (s *Service) ReleaseSpecimen(
	ctx context.Context,
	specimenID string,
	request contracts.ReleaseRequest,
) (domain.ReleaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReleaseRecord{}, err
	}

	transition, err := s.beginReleaseTerminal(ctx, specimenID)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	runtime.Gosched()

	record, err := s.recordReleaseTerminal(ctx, transition, request)
	if err != nil {
		return domain.ReleaseRecord{}, err
	}
	runtime.Gosched()

	if err := s.finalizeReleaseTerminal(ctx, transition); err != nil {
		return domain.ReleaseRecord{}, err
	}
	return record, nil
}
