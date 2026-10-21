package adminapi

import (
	"encoding/json"
	"net/http"

	"github.com/yeomyeonggeori/blueclaw/internal/approvalgate"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

type HeldCallRedemptionHandler struct {
	TaskRunService taskstate.TaskRunStore
}

type heldCallRedemptionResponse struct {
	HoldID string `json:"holdID"`
}

func (handler HeldCallRedemptionHandler) HandleRedeemHeldCall(responseWriter http.ResponseWriter, request *http.Request) {
	if handler.TaskRunService == nil {
		writeApprovalError(responseWriter, http.StatusServiceUnavailable, "the task ledger is not configured")
		return
	}
	var redemption approvalgate.HeldCallRedemption
	if json.NewDecoder(http.MaxBytesReader(responseWriter, request.Body, 1<<20)).Decode(&redemption) != nil {
		writeApprovalError(responseWriter, http.StatusBadRequest, "invalid held call redemption")
		return
	}
	if errorValue := approvalgate.RedeemHeldCall(handler.TaskRunService, redemption); errorValue != nil {
		writeApprovalError(responseWriter, http.StatusConflict, errorValue.Error())
		return
	}
	responseWriter.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(responseWriter).Encode(heldCallRedemptionResponse{HoldID: redemption.HoldID})
}
