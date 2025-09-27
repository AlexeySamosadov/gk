package admin

import (
	cosmos_client "decentralized-api/cosmosclient"
	"fmt"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/productscience/inference/x/inference/types"
	"github.com/stretchr/testify/assert"
	"testing"
)

/*func TestCalculateActiveParticipantsEpoch0(t *testing.T) {
	const (
		// getParticipantsFirstEpochUrl = "http://89.169.103.180:8000/v1/epochs/1/participants"
		// archiveNodeEndpoint          = "http://89.169.103.180:26657"
		getParticipantsFirstEpochUrl = "http://localhost:9000/v1/epochs/1/participants"
		archiveNodeEndpoint          = "http://localhost:26657"
	)
	var genesisBlockHeight = int64(1)

	archiveClient, err := rpcclient.New(archiveNodeEndpoint, "/websocket")
	assert.NoError(t, err)

	validatorsResp, err := archiveClient.Validators(context.Background(), &genesisBlockHeight, nil, nil)
	assert.NoError(t, err)

	req, err := http.NewRequest("GET", getParticipantsFirstEpochUrl, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)

	type participantsResponse struct {
		ActiveParticipants contracts.ActiveParticipants `json:"active_participants"`
	}

	var (
		participantsEpoch1Resp participantsResponse
		participantsEpoch1     contracts.ActiveParticipants
	)
	err = json.Unmarshal(body, &participantsEpoch1Resp)
	assert.NoError(t, err)
	assert.NotEmpty(t, participantsEpoch1Resp.ActiveParticipants.Participants)

	participantsEpoch1 = participantsEpoch1Resp.ActiveParticipants
	participantsEpoch1Data := make(map[string]*contracts.ActiveParticipant)
	for _, participant := range participantsEpoch1.Participants {
		participantsEpoch1Data[strings.ToUpper(participant.ValidatorKey)] = participant
	}

	genesisValidators := validatorsResp.Validators

	participantsEpoch0Data := make([]*contracts.ActiveParticipant, len(genesisValidators))
	for i, validator := range genesisValidators {
		key := base64.StdEncoding.EncodeToString(validator.PubKey.Bytes())
		participant, ok := participantsEpoch1Data[strings.ToUpper(key)]
		if !ok {
			participantsEpoch0Data[i] = &contracts.ActiveParticipant{
				ValidatorKey: key,
			}
		} else {
			participantsEpoch0Data[i] = &contracts.ActiveParticipant{
				Index:        participant.Index,
				ValidatorKey: key,
				Weight:       1,
				InferenceUrl: participant.InferenceUrl,
				Models:       participant.Models,
				MlNodes:      participant.MlNodes,
			}
		}
	}
	participantsEpoch0 := contracts.ActiveParticipants{
		Participants:         participantsEpoch0Data,
		PocStartBlockHeight:  1,
		EffectiveBlockHeight: 1,
		CreatedAtBlockHeight: 1,
	}

	jsonBytes, err := json.Marshal(&participantsEpoch0)
	assert.NoError(t, err)
	fmt.Println(string(jsonBytes))
}*/

func TestMainnet(testing *testing.T) {
	const epoch = 1
	cl, err := cosmos_client.NewRpcClient("http://node2.gonka.ai:26657")
	assert.NoError(testing, err)

	dataKey := types.ActiveParticipantsFullKey(epoch)
	result, err := cosmos_client.QueryByKey(cl, "inference", dataKey)
	assert.NoError(testing, err)

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(interfaceRegistry)
	cdc := codec.NewProtoCodec(interfaceRegistry)

	var activeParticipants types.ActiveParticipants
	err = cdc.Unmarshal(result.Response.Value, &activeParticipants)
	assert.NoError(testing, err)
	fmt.Printf("activeParticipants.CreatedAtBlockHeight %v", activeParticipants.CreatedAtBlockHeight)
	fmt.Printf("activeParticipants.EpochId %v", activeParticipants.EpochId)
	for _, participant := range activeParticipants.Participants {
		fmt.Println(participant)
	}

	result, err = cosmos_client.QueryByKeyWithOptions(cl, "inference", dataKey, activeParticipants.CreatedAtBlockHeight, true)
	assert.NoError(testing, err)

	if result.Response.ProofOps == nil {
		testing.Error("Failed to query active participant. Req 2", types.Participants, "error", err)
	}
	fmt.Println(result.Response.ProofOps)
}
