package domain

import (
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type ReleaseRecord struct {
	ID         string    `json:"id"`
	SpecimenID string    `json:"specimen_id"`
	Purpose    string    `json:"purpose"`
	Recipient  string    `json:"recipient"`
	ApprovedBy string    `json:"approved_by"`
	CreatedAt  time.Time `json:"created_at"`
}

func NewReleaseTransition(
	id string,
	specimen Specimen,
	planID string,
	now time.Time,
) (TerminalTransition, error) {
	if id == "" {
		return TerminalTransition{}, contracts.Internal(
			"missing_generated_id",
			"处理终态标识未生成",
			nil,
		)
	}
	if specimen.ID == "" {
		return TerminalTransition{}, contracts.Internal(
			"missing_specimen_id",
			"标本标识未生成",
			nil,
		)
	}
	return TerminalTransition{
		ID:              id,
		SpecimenID:      specimen.ID,
		PlanID:          planID,
		Kind:            TerminalRelease,
		Phase:           TerminalPrepared,
		SpecimenVersion: specimen.Version,
		CreatedAt:       now.UTC(),
	}, nil
}

func NewReleaseRecordFromTransition(
	id string,
	specimen Specimen,
	transition TerminalTransition,
	purpose string,
	recipient string,
	approvedBy string,
	now time.Time,
) (ReleaseRecord, error) {
	if transition.Kind != TerminalRelease {
		return ReleaseRecord{}, contracts.Conflict(
			"terminal_kind_mismatch",
			"当前处理终态不能形成放行记录",
		)
	}
	if transition.SpecimenID != specimen.ID {
		return ReleaseRecord{}, contracts.Conflict(
			"terminal_subject_mismatch",
			"处理终态对应的标本已经变化",
		)
	}
	purposeValue, err := requiredText("借阅目的", purpose, 200)
	if err != nil {
		return ReleaseRecord{}, err
	}
	recipientValue, err := requiredText("接收方", recipient, 120)
	if err != nil {
		return ReleaseRecord{}, err
	}
	approver, err := requiredText("批准人", approvedBy, 80)
	if err != nil {
		return ReleaseRecord{}, err
	}
	return ReleaseRecord{
		ID:         id,
		SpecimenID: specimen.ID,
		Purpose:    purposeValue,
		Recipient:  recipientValue,
		ApprovedBy: approver,
		CreatedAt:  now.UTC(),
	}, nil
}

func NewReleaseRecord(
	id string,
	specimen Specimen,
	purpose string,
	recipient string,
	approvedBy string,
	now time.Time,
) (ReleaseRecord, error) {
	if id == "" {
		return ReleaseRecord{}, contracts.Internal("missing_generated_id", "放行标识未生成", nil)
	}
	if !specimen.IsStable() {
		return ReleaseRecord{}, contracts.Conflict("specimen_not_releasable", "只有已稳定标本可以办理放行")
	}
	purposeValue, err := requiredText("借阅目的", purpose, 200)
	if err != nil {
		return ReleaseRecord{}, err
	}
	recipientValue, err := requiredText("接收方", recipient, 120)
	if err != nil {
		return ReleaseRecord{}, err
	}
	approver, err := requiredText("批准人", approvedBy, 80)
	if err != nil {
		return ReleaseRecord{}, err
	}
	return ReleaseRecord{
		ID:         id,
		SpecimenID: specimen.ID,
		Purpose:    purposeValue,
		Recipient:  recipientValue,
		ApprovedBy: approver,
		CreatedAt:  now.UTC(),
	}, nil
}
