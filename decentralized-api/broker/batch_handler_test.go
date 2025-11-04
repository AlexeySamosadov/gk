package broker

import (
	"decentralized-api/cosmosclient"
	"decentralized-api/mlnodeclient"
	"testing"

	"github.com/productscience/inference/api/inference/inference"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestBatchHandler_HandleGeneratedBatch(t *testing.T) {
	tests := []struct {
		name          string
		nodeID        string
		batch         mlnodeclient.ProofBatch
		expectError   bool
		setupMock     func(*cosmosclient.MockCosmosMessageClient)
	}{
		{
			name:   "successful batch with nodeID",
			nodeID: "test-node-123",
			batch: mlnodeclient.ProofBatch{
				BlockHeight: 100,
				NodeNum:     1,
				Nonces:      []int64{1, 2, 3},
				Dist:        []float64{0.1, 0.2, 0.3},
			},
			expectError: false,
			setupMock: func(m *cosmosclient.MockCosmosMessageClient) {
				m.On("SubmitPocBatch", mock.MatchedBy(func(msg *inference.MsgSubmitPocBatch) bool {
					return msg.PocStageStartBlockHeight == 100 &&
						msg.NodeId == "test-node-123" &&
						len(msg.Nonces) == 3 &&
						len(msg.Dist) == 3
				})).Return(nil)
			},
		},
		{
			name:   "successful batch with empty nodeID",
			nodeID: "",
			batch: mlnodeclient.ProofBatch{
				BlockHeight: 100,
				NodeNum:     1,
				Nonces:      []int64{1, 2, 3},
				Dist:        []float64{0.1, 0.2, 0.3},
			},
			expectError: false,
			setupMock: func(m *cosmosclient.MockCosmosMessageClient) {
				m.On("SubmitPocBatch", mock.MatchedBy(func(msg *inference.MsgSubmitPocBatch) bool {
					return msg.PocStageStartBlockHeight == 100 &&
						msg.NodeId == "" &&
						len(msg.Nonces) == 3 &&
						len(msg.Dist) == 3
				})).Return(nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRecorder := &cosmosclient.MockCosmosMessageClient{}
			if tt.setupMock != nil {
				tt.setupMock(mockRecorder)
			}

			handler := NewBatchHandler(mockRecorder)
			err := handler.HandleGeneratedBatch(tt.nodeID, tt.batch)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockRecorder.AssertExpectations(t)
		})
	}
}

func TestBatchHandler_HandleValidatedBatch(t *testing.T) {
	validPubKey := "02a1633cafcc01ebfb6d78e39f687a1f0995c62fc95f51ead10a02ee0be551b5dc"
	
	tests := []struct {
		name        string
		nodeID      string
		batch       mlnodeclient.ValidatedBatch
		expectError bool
		setupMock   func(*cosmosclient.MockCosmosMessageClient)
	}{
		{
			name:   "successful validated batch",
			nodeID: "test-node-123",
			batch: mlnodeclient.ValidatedBatch{
				ProofBatch: mlnodeclient.ProofBatch{
					BlockHeight: 100,
					PublicKey:   validPubKey,
					Nonces:      []int64{1, 2, 3},
					Dist:        []float64{0.1, 0.2, 0.3},
				},
				ReceivedDist:      []float64{0.1, 0.2, 0.3},
				RTarget:           0.5,
				FraudThreshold:    0.1,
				NInvalid:          0,
				ProbabilityHonest: 1.0,
				FraudDetected:     false,
			},
			expectError: false,
			setupMock: func(m *cosmosclient.MockCosmosMessageClient) {
				m.On("SubmitPoCValidation", mock.MatchedBy(func(msg *inference.MsgSubmitPocValidation) bool {
					return msg.PocStageStartBlockHeight == 100 &&
						msg.NInvalid == 0 &&
						msg.FraudDetected == false &&
						len(msg.Nonces) == 0 &&
						len(msg.Dist) == 0 &&
						len(msg.ReceivedDist) == 0
				})).Return(nil)
			},
		},
		{
			name:   "validated batch with empty nodeID (nodeID not used for validation)",
			nodeID: "",
			batch: mlnodeclient.ValidatedBatch{
				ProofBatch: mlnodeclient.ProofBatch{
					BlockHeight: 100,
					PublicKey:   validPubKey,
					Nonces:      []int64{1, 2, 3},
					Dist:        []float64{0.1, 0.2, 0.3},
				},
				ReceivedDist:      []float64{0.1, 0.2, 0.3},
				RTarget:           0.5,
				FraudThreshold:    0.1,
				NInvalid:          1,
				ProbabilityHonest: 0.9,
				FraudDetected:     true,
			},
			expectError: false,
			setupMock: func(m *cosmosclient.MockCosmosMessageClient) {
				m.On("SubmitPoCValidation", mock.MatchedBy(func(msg *inference.MsgSubmitPocValidation) bool {
					return msg.PocStageStartBlockHeight == 100 &&
						msg.NInvalid == 1 &&
						msg.FraudDetected == true
				})).Return(nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRecorder := &cosmosclient.MockCosmosMessageClient{}
			if tt.setupMock != nil {
				tt.setupMock(mockRecorder)
			}

			handler := NewBatchHandler(mockRecorder)
			err := handler.HandleValidatedBatch(tt.batch)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockRecorder.AssertExpectations(t)
		})
	}
}

