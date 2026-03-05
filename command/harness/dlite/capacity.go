package dlite

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/harness"
	"github.com/drone-runners/drone-runner-aws/types"
)

// Capacity task constants.
const (
	capacityTimeoutMs = 2 * 60 * 1000
)

// VMCapacityTask handles VM capacity reservation tasks.
type VMCapacityTask struct {
	c *dliteCommand
}

// VMCapacityRequest represents the request payload for capacity reservation.
type VMCapacityRequest struct {
	CapacityReservationRequest harness.CapacityReservationRequest `json:"capacity_reservation_request"`
}

// ServeHTTP handles the VM capacity task request.
func (t *VMCapacityTask) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, taskBytes, logr, ok := decodeTask(w, r)
	if !ok {
		return
	}

	req := &VMCapacityRequest{}
	if !unmarshalTaskRequest(w, taskBytes, req, logr) {
		return
	}

	// Determine timeout.
	var timeout int64 = capacityTimeoutMs
	if req.CapacityReservationRequest.ReservationTimeout > 0 {
		timeout = req.CapacityReservationRequest.ReservationTimeout
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Millisecond)
	defer cancel()

	accountID := harness.GetAccountID(&req.CapacityReservationRequest.Context, map[string]string{})

	// Execute capacity reservation.
	vmService := t.c.getVMService()
	capacityReservationResponse, err := vmService.ReserveCapacity(ctx, &req.CapacityReservationRequest)
	if err != nil {
		t.c.runner.Metrics.ErrorCount.WithLabelValues(accountID, strconv.FormatBool(true)).Inc()
		logr.WithError(err).WithField("account_id", accountID).Error("could not reserve capacity for VM")
		writeErrorResponse(w, err)
		return
	}

	// Ensure non-nil response.
	if capacityReservationResponse == nil {
		capacityReservationResponse = &types.CapacityReservation{}
	}

	// Construct success response.
	resp := VMTaskExecutionResponse{
		CommandExecutionStatus: Success,
		CapacityReservation:    *capacityReservationResponse,
	}

	writeSuccessResponse(w, &resp)
}
