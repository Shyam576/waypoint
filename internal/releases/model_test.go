package releases

import "testing"

func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to Stage
		want     bool
	}{
		{StageDraft, StageDevelopment, true},
		{StageDraft, StageClosed, true},
		{StageDraft, StageUAT, false},
		{StageDraft, StagePostReleaseSupport, false},
		{StageDevelopment, StageUAT, true},
		{StageDevelopment, StageDraft, true},
		{StageUAT, StageReadyForProduction, true},
		{StageUAT, StageProduction, false},
		{StageReadyForProduction, StageProduction, true},
		{StageProduction, StagePostReleaseSupport, true},
		{StageProduction, StageDraft, false},
		{StagePostReleaseSupport, StageClosed, true},
		{StageClosed, StageDraft, false},
		{StageClosed, StageClosed, false},
	}

	for _, c := range cases {
		got := CanTransition(c.from, c.to)
		if got != c.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestRiskLevelValid(t *testing.T) {
	for _, r := range []RiskLevel{RiskLow, RiskMedium, RiskHigh} {
		if !r.Valid() {
			t.Errorf("expected %s to be valid", r)
		}
	}
	if RiskLevel("critical").Valid() {
		t.Error("expected 'critical' to be invalid")
	}
}
