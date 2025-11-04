package mlnode

import (
	"decentralized-api/logging"
	"net/http"

	"decentralized-api/mlnodeclient"

	"github.com/labstack/echo/v4"
	"github.com/productscience/inference/x/inference/types"
)

func (s *Server) postGeneratedBatches(ctx echo.Context) error {
	var body mlnodeclient.ProofBatch

	if err := ctx.Bind(&body); err != nil {
		logging.Error("ProofBatch-callback. Failed to decode request body of type ProofBatch", types.PoC, "error", err)
		return echo.NewHTTPError(http.StatusBadRequest, err)
	}

	nodeID := ""
	node, found := s.broker.GetNodeByNodeNum(body.NodeNum)
	if found {
		nodeID = node.Id
		logging.Info("ProofBatch-callback. Found node by node num", types.PoC,
			"nodeId", nodeID,
			"nodeNum", body.NodeNum)
	} else {
		logging.Warn("ProofBatch-callback. Unknown NodeNum. Sending MsgSubmitPocBatch with empty nodeId",
			types.PoC, "node_num", body.NodeNum)
	}

	if err := s.batchHandler.HandleGeneratedBatch(nodeID, body); err != nil {
		return err
	}

	return ctx.NoContent(http.StatusOK)
}

func (s *Server) postValidatedBatches(ctx echo.Context) error {
	var body mlnodeclient.ValidatedBatch

	if err := ctx.Bind(&body); err != nil {
		logging.Error("ValidateReceivedBatches-callback. Failed to decode request body of type ValidatedBatch", types.PoC, "error", err)
		return echo.NewHTTPError(http.StatusBadRequest, err)
	}

	if err := s.batchHandler.HandleValidatedBatch(body); err != nil {
		return err
	}

	return ctx.NoContent(http.StatusOK)
}
