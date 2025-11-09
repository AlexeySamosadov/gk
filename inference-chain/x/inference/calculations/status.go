package calculations

import (
	"github.com/productscience/inference/x/inference/types"
	"github.com/shopspring/decimal"
)

type ParticipantStatusReason string

const (
	// ConsecutiveFailures indicates the participant has too many consecutive failures
	ConsecutiveFailures ParticipantStatusReason = "consecutive_failures"
	// Ramping indicates the participant is in ramp-up phase
	Ramping ParticipantStatusReason = "ramping"
	// StatisticalInvalidations indicates the participant has statistically significant invalidations
	StatisticalInvalidations ParticipantStatusReason = "statistical_invalidations"
	// NoSpecificReason indicates no specific reason for the status
	NoSpecificReason ParticipantStatusReason = ""
	// AlgorithmError Should NEVER happen unless we have bad algorithms or parameters
	AlgorithmError ParticipantStatusReason = "algorithm_error"
	// AlreadySet when we are already invalid or inactive
	AlreadySet ParticipantStatusReason = "already_set"
	// Downtime when missed inferences exceeds the threshold
	Downtime ParticipantStatusReason = "downtime"
)

const (
	// Keeping the log precision low keeps compute low and high precision is not needed
	LogPrecision = 12
)

// Note that newValue is passed in BY VALUE, so changes to newValue directly will not pass back
func ComputeStatus(validationParameters *types.ValidationParams, newValue types.Participant, oldStats types.CurrentEpochStats) (status types.ParticipantStatus, reason ParticipantStatusReason, stats types.CurrentEpochStats) {
	// Genesis only (for tests)
	newStats := getStats(&newValue)
	if validationParameters == nil || validationParameters.FalsePositiveRate == nil {
		return types.ParticipantStatus_ACTIVE, NoSpecificReason, newStats
	}

	// Once INVALID or INACTIVE, this can only be reset deliberately (at epoch start)
	if newValue.Status == types.ParticipantStatus_INVALID || newValue.Status == types.ParticipantStatus_INACTIVE {
		return newValue.Status, AlreadySet, newStats
	}

	// If we have consecutive failures with a likelihood of less than 1 in a million times, we're assuming bad
	falsePositiveRate := validationParameters.FalsePositiveRate.ToDecimal()
	consecutiveFailureCutoff := validationParameters.QuickFailureThreshold.ToDecimal()
	if probabilityOfConsecutiveFailures(falsePositiveRate, newValue.ConsecutiveInvalidInferences).LessThan(consecutiveFailureCutoff) {
		return types.ParticipantStatus_INVALID, ConsecutiveFailures, newStats
	}

	invalidationDecision := getInvalidationStatus(&newStats, oldStats, validationParameters)
	if invalidationDecision == Fail {
		return types.ParticipantStatus_INVALID, StatisticalInvalidations, newStats
	} else if invalidationDecision == Error {
		return types.ParticipantStatus_ACTIVE, AlgorithmError, newStats
	}

	inactiveDecision := getInactiveStatus(&newStats, oldStats, validationParameters)
	if inactiveDecision == Fail {
		return types.ParticipantStatus_INACTIVE, Downtime, newStats
	} else if inactiveDecision == Error {
		return types.ParticipantStatus_ACTIVE, AlgorithmError, newStats
	}

	return types.ParticipantStatus_ACTIVE, NoSpecificReason, newStats
}

func getInactiveStatus(newStats *types.CurrentEpochStats, oldStats types.CurrentEpochStats, parameters *types.ValidationParams) Decision {
	newInferences := int64(newStats.InferenceCount) - int64(oldStats.InferenceCount)
	newMissedInferences := int64(newStats.MissedRequests) - int64(oldStats.MissedRequests)
	inactiveSprt, err := NewSPRT(
		parameters.DowntimeGoodPercentage.ToDecimal(),
		parameters.DowntimeBadPercentage.ToDecimal(),
		parameters.DowntimeHThreshold.ToDecimal(),
		newStats.InactiveLLR.ToDecimal(),
		LogPrecision,
	)
	if err != nil {
		return Error
	}
	inactiveSprt.UpdateCounts(newMissedInferences, newInferences)
	newStats.InactiveLLR = types.DecimalFromDecimal(inactiveSprt.LLR)
	return inactiveSprt.Decision()
}

func getInvalidationStatus(newStats *types.CurrentEpochStats, oldStats types.CurrentEpochStats, parameters *types.ValidationParams) Decision {
	newValidations := int64(newStats.ValidatedInferences) - int64(oldStats.ValidatedInferences)
	newInvalidations := int64(newStats.InvalidatedInferences) - int64(oldStats.InvalidatedInferences)
	//newInferences := newValue.CurrentEpochStats.InferenceCount - oldValue.CurrentEpochStats.InferenceCount
	//newMissedInferences := newValue.CurrentEpochStats.MissedRequests - oldValue.CurrentEpochStats.MissedRequests

	invalidationSprt, err := NewSPRT(
		parameters.FalsePositiveRate.ToDecimal(),
		parameters.BadParticipantInvalidationRate.ToDecimal(),
		parameters.InvalidationHThreshold.ToDecimal(),
		newStats.InvalidLLR.ToDecimal(),
		LogPrecision,
	)
	if err != nil {
		return Error
	}
	invalidationSprt.UpdateCounts(newInvalidations, newValidations)
	newStats.InvalidLLR = types.DecimalFromDecimal(invalidationSprt.LLR)
	return invalidationSprt.Decision()
}

func getStats(newValue *types.Participant) types.CurrentEpochStats {
	var newStats types.CurrentEpochStats
	if newValue == nil || newValue.CurrentEpochStats == nil {
		newStats = types.CurrentEpochStats{}
	} else {
		newStats = *newValue.CurrentEpochStats
	}
	if newStats.InvalidLLR == nil {
		newStats.InvalidLLR = &types.Decimal{
			Value:    0,
			Exponent: 0,
		}
	}
	if newStats.InactiveLLR == nil {
		newStats.InactiveLLR = &types.Decimal{
			Value:    0,
			Exponent: 0,
		}
	}
	return newStats
}

// probabilityOfConsecutiveFailures returns P(F^N|G) = x^N
func probabilityOfConsecutiveFailures(expectedFailureRate decimal.Decimal, consecutiveFailures int64) decimal.Decimal {
	if expectedFailureRate.LessThan(decimal.Zero) || expectedFailureRate.GreaterThan(decimal.NewFromInt(1)) {
		// This won't happen
		return decimal.Zero
	}
	if consecutiveFailures < 0 {
		return decimal.Zero
	}

	return expectedFailureRate.Pow(decimal.NewFromInt(consecutiveFailures))
}
