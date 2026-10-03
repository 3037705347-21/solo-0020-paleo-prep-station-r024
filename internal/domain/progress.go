package domain

import (
	"fmt"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type ProgressEntry struct {
	ID         string    `json:"id"`
	PlanID     string    `json:"plan_id"`
	Sequence   int       `json:"sequence"`
	Stage      string    `json:"stage"`
	Percent    int       `json:"percent"`
	Note       string    `json:"note"`
	RecordedBy string    `json:"recorded_by"`
	CreatedAt  time.Time `json:"created_at"`
}

type TerminalKind string

const (
	TerminalProgress TerminalKind = "progress"
	TerminalRelease  TerminalKind = "release"
)

const (
	TerminalPrepared = "prepared"
	TerminalRecorded = "recorded"
)

type TerminalTransition struct {
	ID              string       `json:"id"`
	SpecimenID      string       `json:"specimen_id"`
	PlanID          string       `json:"plan_id,omitempty"`
	Kind            TerminalKind `json:"kind"`
	Phase           string       `json:"phase"`
	SpecimenVersion int          `json:"specimen_version"`
	PlanVersion     int          `json:"plan_version,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
}

func NewProgressTransition(
	id string,
	plan PrepPlan,
	specimen Specimen,
	now time.Time,
) (TerminalTransition, error) {
	if id == "" {
		return TerminalTransition{}, contracts.Internal(
			"missing_generated_id",
			"处理终态标识未生成",
			nil,
		)
	}
	if plan.SpecimenID != specimen.ID {
		return TerminalTransition{}, contracts.Conflict(
			"terminal_specimen_mismatch",
			"方案与标本不属于同一处理终态",
		)
	}
	if plan.Status != PlanApproved && plan.Status != PlanActive {
		return TerminalTransition{}, contracts.Conflict(
			"plan_not_recordable",
			"当前方案状态不能追加制备进度",
		)
	}
	return TerminalTransition{
		ID:              id,
		SpecimenID:      specimen.ID,
		PlanID:          plan.ID,
		Kind:            TerminalProgress,
		Phase:           TerminalPrepared,
		SpecimenVersion: specimen.Version,
		PlanVersion:     plan.Version,
		CreatedAt:       now.UTC(),
	}, nil
}

func (t TerminalTransition) ValidateProgress(
	plan PrepPlan,
	specimen Specimen,
) error {
	if t.Kind != TerminalProgress {
		return contracts.Conflict("terminal_kind_mismatch", "当前处理终态不是进度提交")
	}
	if t.SpecimenID != specimen.ID || t.PlanID != plan.ID {
		return contracts.Conflict("terminal_subject_mismatch", "处理终态对应的对象已经变化")
	}
	if t.Phase != TerminalPrepared {
		return contracts.Conflict("terminal_phase_changed", "处理终态已经进入下一阶段")
	}
	if plan.Status != PlanApproved && plan.Status != PlanActive {
		return contracts.Conflict("plan_not_recordable", "当前方案状态不能追加制备进度")
	}
	return nil
}

func (t *TerminalTransition) MarkRecorded(now time.Time) error {
	if t.Phase != TerminalPrepared {
		return contracts.Conflict("terminal_phase_changed", "处理终态已经进入下一阶段")
	}
	t.Phase = TerminalRecorded
	return nil
}

func NewProgressEntry(
	id string,
	plan PrepPlan,
	previous []ProgressEntry,
	stage string,
	percent int,
	note string,
	recordedBy string,
	now time.Time,
) (ProgressEntry, error) {
	if id == "" {
		return ProgressEntry{}, contracts.Internal("missing_generated_id", "进度标识未生成", nil)
	}
	if plan.Status != PlanApproved && plan.Status != PlanActive {
		return ProgressEntry{}, contracts.Conflict("plan_not_recordable", "当前方案状态不能追加制备进度")
	}
	if err := validatePercent(percent); err != nil {
		return ProgressEntry{}, err
	}
	stageValue, err := requiredText("阶段名称", stage, 80)
	if err != nil {
		return ProgressEntry{}, err
	}
	noteValue, err := optionalText("进度说明", note, 500)
	if err != nil {
		return ProgressEntry{}, err
	}
	recorder, err := requiredText("记录人", recordedBy, 80)
	if err != nil {
		return ProgressEntry{}, err
	}
	if len(previous) > 0 && percent < previous[len(previous)-1].Percent {
		return ProgressEntry{}, contracts.Conflict("progress_regressed", "新的进度比例不能低于上一条记录")
	}
	return ProgressEntry{
		ID:         id,
		PlanID:     plan.ID,
		Sequence:   len(previous) + 1,
		Stage:      stageValue,
		Percent:    percent,
		Note:       noteValue,
		RecordedBy: recorder,
		CreatedAt:  now.UTC(),
	}, nil
}

func (p ProgressEntry) String() string {
	return fmt.Sprintf("%s#%d(%d%%)", p.PlanID, p.Sequence, p.Percent)
}
