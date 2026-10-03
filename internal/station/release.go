package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

// ReleaseSpecimen 在同一个仓储事务内办理研究放行：重新核对标本已稳定且不
// 存在未结束方案，随后同时写入不可覆盖的放行记录并把标本置为 available。
// 前置条件不满足或请求内容不合法时整个事务回滚，不会留下悬挂的处理终态，
// 因此一次失败的放行尝试不会阻碍后续正常的进度记录或再次放行。
func (s *Service) ReleaseSpecimen(
	ctx context.Context,
	specimenID string,
	request contracts.ReleaseRequest,
) (domain.ReleaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReleaseRecord{}, err
	}

	var created domain.ReleaseRecord
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
			return notFoundSpecimen(specimenID)
		}
		if !specimen.IsStable() {
			return contracts.Conflict(
				"specimen_not_releasable",
				"只有已稳定标本可以办理放行",
			)
		}
		if m.HasOpenPlan(specimenID) {
			return contracts.Conflict(
				"open_plan_exists",
				"存在未结束的修复方案，不能办理放行",
			)
		}
		if _, exists := m.ReleaseForSpecimen(specimen.ID); exists {
			return contracts.Conflict(
				"release_exists",
				"该标本已经存在放行记录",
			)
		}
		record, err := domain.NewReleaseRecord(
			s.nextID("release"),
			specimen,
			request.Purpose,
			request.Recipient,
			request.ApprovedBy,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := specimen.MarkAvailable(s.now()); err != nil {
			return err
		}
		m.PutRelease(record)
		m.PutSpecimen(specimen)
		created = record
		return nil
	})
	return created, err
}
