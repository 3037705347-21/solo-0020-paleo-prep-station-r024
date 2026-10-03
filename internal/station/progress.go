package station

import (
	"context"
	"runtime"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) beginProgressTerminal(
	ctx context.Context,
	planID string,
) (domain.TerminalTransition, error) {
	if err := ctx.Err(); err != nil {
		return domain.TerminalTransition{}, err
	}

	var transition domain.TerminalTransition
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		specimen, ok := m.GetSpecimen(plan.SpecimenID)
		if !ok {
			return notFoundSpecimen(plan.SpecimenID)
		}
		if existing, ok := m.TransitionForSpecimen(specimen.ID); ok {
			if existing.Kind != domain.TerminalProgress ||
				existing.PlanID != plan.ID {
				return contracts.Conflict(
					"terminal_transition_pending",
					"该标本已有其他处理终态正在确认",
				)
			}
			transition = existing
			return nil
		}
		value, err := domain.NewProgressTransition(
			s.nextID("transition"),
			plan,
			specimen,
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

func (s *Service) finishProgressTerminal(
	ctx context.Context,
	transition domain.TerminalTransition,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.repo.Mutate(func(m *repository.Mutation) error {
		current, ok := m.TransitionForSpecimen(transition.SpecimenID)
		if !ok {
			return nil
		}
		if current.ID != transition.ID ||
			current.Kind != domain.TerminalProgress {
			return contracts.Conflict(
				"terminal_transition_changed",
				"处理终态已被其他请求取代",
			)
		}
		plan, ok := m.GetPlan(transition.PlanID)
		if !ok {
			return notFoundPlan(transition.PlanID)
		}
		specimen, ok := m.GetSpecimen(transition.SpecimenID)
		if !ok {
			return notFoundSpecimen(transition.SpecimenID)
		}
		if !plan.IsCompleted() || !specimen.IsStable() {
			return contracts.Conflict(
				"terminal_projection_incomplete",
				"处理终态尚未形成完整结果",
			)
		}
		m.DeleteTransition(transition.SpecimenID)
		return nil
	})
}

func (s *Service) LogProgress(
	ctx context.Context,
	planID string,
	request contracts.ProgressRequest,
) (domain.ProgressEntry, error) {
	if err := ctx.Err(); err != nil {
		return domain.ProgressEntry{}, err
	}

	var transition domain.TerminalTransition
	if request.Percent == 100 {
		value, beginErr := s.beginProgressTerminal(ctx, planID)
		if beginErr != nil {
			return domain.ProgressEntry{}, beginErr
		}
		transition = value
		runtime.Gosched()
	}

	var created domain.ProgressEntry
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		specimen, ok := m.GetSpecimen(plan.SpecimenID)
		if !ok {
			return notFoundSpecimen(plan.SpecimenID)
		}
		if request.Percent == 100 {
			if err := transition.ValidateProgress(plan, specimen); err != nil {
				return err
			}
		}
		previous := m.ProgressForPlan(planID)
		entry, err := domain.NewProgressEntry(
			s.nextID("progress"),
			plan,
			previous,
			request.Stage,
			request.Percent,
			request.Note,
			request.RecordedBy,
			s.now(),
		)
		if err != nil {
			return err
		}
		if plan.Status == domain.PlanApproved {
			if err := plan.Begin(s.now()); err != nil {
				return err
			}
		}
		if request.Percent == 100 {
			if err := plan.Complete(s.now()); err != nil {
				return err
			}
			if err := specimen.MarkStabilized(s.now()); err != nil {
				return err
			}
		}
		m.AppendProgress(entry)
		m.PutPlan(plan)
		m.PutSpecimen(specimen)
		created = entry
		return nil
	})
	if err == nil && request.Percent == 100 {
		err = s.finishProgressTerminal(ctx, transition)
	}
	return created, err
}
