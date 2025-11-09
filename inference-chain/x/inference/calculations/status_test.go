package calculations

import (
	"testing"

	"github.com/productscience/inference/x/inference/types"
	"github.com/stretchr/testify/require"
)

var zeroStats = types.CurrentEpochStats{
	InvalidLLR:  types.DecimalFromFloat(0),
	InactiveLLR: types.DecimalFromFloat(0),
}

func TestComputeStatus(t *testing.T) {
	tests := []struct {
		name        string
		params      *types.ValidationParams
		participant types.Participant
		wantStatus  types.ParticipantStatus
		wantReason  ParticipantStatusReason
	}{
		{
			name:        "nil validation parameters returns active",
			params:      nil,
			participant: types.Participant{},
			wantStatus:  types.ParticipantStatus_ACTIVE,
			wantReason:  NoSpecificReason,
		},
		{
			name: "consecutive failures returns invalid",
			params: &types.ValidationParams{
				FalsePositiveRate:              types.DecimalFromFloat(0.05),
				BadParticipantInvalidationRate: types.DecimalFromFloat(0.1),
				InvalidationHThreshold:         types.DecimalFromFloat(4),
				DowntimeGoodPercentage:         types.DecimalFromFloat(0.1),
				DowntimeBadPercentage:          types.DecimalFromFloat(0.2),
				DowntimeHThreshold:             types.DecimalFromFloat(4),
				QuickFailureThreshold:          types.DecimalFromFloat(0.000001),
			},
			participant: types.Participant{
				ConsecutiveInvalidInferences: 20,
			},
			wantStatus: types.ParticipantStatus_INVALID,
			wantReason: ConsecutiveFailures,
		},
		{
			name: "statistical invalidations returns invalid",
			params: &types.ValidationParams{
				BadParticipantInvalidationRate: types.DecimalFromFloat(0.1),
				InvalidationHThreshold:         types.DecimalFromFloat(4),
				FalsePositiveRate:              types.DecimalFromFloat(0.05),
				DowntimeGoodPercentage:         types.DecimalFromFloat(0.1),
				DowntimeBadPercentage:          types.DecimalFromFloat(0.2),
				DowntimeHThreshold:             types.DecimalFromFloat(4),
				QuickFailureThreshold:          types.DecimalFromFloat(0.000001),
			},
			participant: types.Participant{
				CurrentEpochStats: &types.CurrentEpochStats{
					ValidatedInferences:   7,
					InvalidatedInferences: 7,
				},
			},
			wantStatus: types.ParticipantStatus_INVALID,
			wantReason: StatisticalInvalidations,
		},
		{
			name: "normal operation returns active",
			params: &types.ValidationParams{
				BadParticipantInvalidationRate: types.DecimalFromFloat(0.1),
				InvalidationHThreshold:         types.DecimalFromFloat(4),
				FalsePositiveRate:              types.DecimalFromFloat(0.05),
				DowntimeGoodPercentage:         types.DecimalFromFloat(0.1),
				DowntimeBadPercentage:          types.DecimalFromFloat(0.2),
				DowntimeHThreshold:             types.DecimalFromFloat(4),
				QuickFailureThreshold:          types.DecimalFromFloat(0.000001),
			},
			participant: types.Participant{
				CurrentEpochStats: &types.CurrentEpochStats{
					ValidatedInferences:   95,
					InvalidatedInferences: 5,
				},
			},
			wantStatus: types.ParticipantStatus_ACTIVE,
			wantReason: NoSpecificReason,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, reason, _ := ComputeStatus(tt.params, tt.participant, zeroStats)
			require.Equal(t, tt.wantStatus, status)
			require.Equal(t, tt.wantReason, reason)
		})
	}
}

func TestDowntimeTriggersInactive(t *testing.T) {
	params := &types.ValidationParams{
		FalsePositiveRate:              types.DecimalFromFloat(0.05),
		BadParticipantInvalidationRate: types.DecimalFromFloat(0.1),
		InvalidationHThreshold:         types.DecimalFromFloat(4),
		DowntimeGoodPercentage:         types.DecimalFromFloat(0.1), // P0
		DowntimeBadPercentage:          types.DecimalFromFloat(0.2), // P1
		DowntimeHThreshold:             types.DecimalFromFloat(4),   // H
		QuickFailureThreshold:          types.DecimalFromFloat(0.000001),
	}

	participant := types.Participant{
		CurrentEpochStats: &types.CurrentEpochStats{
			InferenceCount:        50, // passes
			MissedRequests:        60, // failures
			ValidatedInferences:   0,
			InvalidatedInferences: 0,
		},
	}

	status, reason, _ := ComputeStatus(params, participant, zeroStats)
	require.Equal(t, types.ParticipantStatus_INACTIVE, status)
	require.Equal(t, Downtime, reason)
}

func TestDowntimeParamsOutOfRangeReturnAlgorithmError(t *testing.T) {
	badVals := []struct{ good, bad float64 }{
		{0, 0.2},    // good == 0
		{1, 0.2},    // good == 1
		{-0.1, 0.2}, // good < 0
		{0.1, 0},    // bad == 0
		{0.1, 1},    // bad == 1
		{0.1, 1.1},  // bad > 1
	}

	for _, v := range badVals {
		params := &types.ValidationParams{
			FalsePositiveRate:              types.DecimalFromFloat(0.05),
			BadParticipantInvalidationRate: types.DecimalFromFloat(0.1),
			InvalidationHThreshold:         types.DecimalFromFloat(4),
			DowntimeGoodPercentage:         types.DecimalFromFloat(v.good),
			DowntimeBadPercentage:          types.DecimalFromFloat(v.bad),
			DowntimeHThreshold:             types.DecimalFromFloat(4),
			QuickFailureThreshold:          types.DecimalFromFloat(0.000001),
		}
		participant := types.Participant{CurrentEpochStats: &types.CurrentEpochStats{}}
		status, reason, _ := ComputeStatus(params, participant, zeroStats)
		require.Equal(t, types.ParticipantStatus_ACTIVE, status)
		require.Equal(t, AlgorithmError, reason)
	}
}

func TestProbabilityOfConsecutiveFailures_PanicOnBadRate(t *testing.T) {
	// expectedFailureRate must be in [0,1]
	defer func() { _ = recover() }()
	_ = probabilityOfConsecutiveFailures(types.DecimalFromFloat(1.1).ToDecimal(), 3)
}
