package harness

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/httphelper"

	"github.com/drone-runners/drone-runner-aws/app/httprender"
	errors "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
)

// HTTPHandlers provides HTTP handlers backed by a VMService.
type HTTPHandlers struct {
	service *VMService
}

// NewHTTPHandlers creates a new HTTPHandlers instance.
func NewHTTPHandlers(service *VMService) *HTTPHandlers {
	return &HTTPHandlers{service: service}
}

// Router creates a chi router with all VM handlers registered.
func (h *HTTPHandlers) Router() http.Handler {
	mux := chi.NewMux()
	mux.Use(Middleware)

	mux.Post("/pool_owner", h.HandlePoolOwner)
	mux.Post("/setup", h.HandleSetup)
	mux.Post("/destroy", h.HandleDestroy)
	mux.Post("/step", h.HandleStep)
	mux.Post("/suspend", h.HandleSuspend)
	mux.Mount("/metrics", promhttp.Handler())
	mux.Get("/healthz", h.HandleHealthz)

	return mux
}

// HandleHealthz handles health check requests.
func (h *HTTPHandlers) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, "OK") //nolint:errcheck
}

// HandlePoolOwner handles pool ownership check requests.
func (h *HTTPHandlers) HandlePoolOwner(w http.ResponseWriter, r *http.Request) {
	poolName := r.URL.Query().Get("pool")
	if poolName == "" {
		httprender.BadRequest(w, "mandatory URL parameter 'pool' is missing", nil)
		return
	}

	type poolOwnerResponse struct {
		Owner bool `json:"owner"`
	}

	if !h.service.PoolExists(poolName) {
		httprender.OK(w, poolOwnerResponse{Owner: false})
		return
	}

	stageID := r.URL.Query().Get("stageId")
	if stageID != "" {
		entity, err := h.service.FindStageOwner(r.Context(), stageID)
		if err != nil {
			logrus.WithError(err).
				WithField("pool", poolName).
				WithField("stageId", stageID).
				Error("failed to find the stage in store")
			httprender.OK(w, poolOwnerResponse{Owner: false})
			return
		}

		if entity.PoolName != poolName {
			logrus.WithField("pool", poolName).
				WithField("stageId", stageID).
				Errorf("found stage with different pool: %s", entity.PoolName)
			httprender.OK(w, poolOwnerResponse{Owner: false})
			return
		}
	}

	httprender.OK(w, poolOwnerResponse{Owner: true})
}

// HandleSetup handles VM setup requests.
func (h *HTTPHandlers) HandleSetup(w http.ResponseWriter, r *http.Request) {
	req := &SetupVMRequest{}
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		logrus.WithError(err).Error("could not decode request body")
		httprender.BadRequest(w, err.Error(), nil)
		return
	}

	resp, _, err := h.service.Setup(r.Context(), req)
	if err != nil {
		logrus.WithField("stage_runtime_id", req.ID).WithError(err).Error("could not setup VM")
		writeError(w, err)
		return
	}

	httprender.OK(w, resp)
}

// HandleStep handles VM step execution requests.
func (h *HTTPHandlers) HandleStep(w http.ResponseWriter, r *http.Request) {
	req := &ExecuteVMRequest{}
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		logrus.WithError(err).Error("could not decode VM step execute request body")
		httprender.BadRequest(w, err.Error(), nil)
		return
	}

	resp, err := h.service.Step(r.Context(), req, false)
	if err != nil {
		logrus.WithField("stage_runtime_id", req.StageRuntimeID).
			WithField("step_id", req.ID).
			WithError(err).Error("could not execute step on VM")
		writeError(w, err)
		return
	}

	httprender.OK(w, resp)
}

// HandleSuspend handles VM suspend requests.
func (h *HTTPHandlers) HandleSuspend(w http.ResponseWriter, r *http.Request) {
	req := &SuspendVMRequest{}
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		logrus.WithError(err).Error("could not decode suspend request body")
		httprender.BadRequest(w, err.Error(), nil)
		return
	}

	err := h.service.Suspend(r.Context(), req)
	if err != nil {
		logrus.WithField("stage_runtime_id", req.StageRuntimeID).
			WithError(err).Error("could not suspend VM")
		writeError(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// HandleDestroy handles VM destroy requests.
func (h *HTTPHandlers) HandleDestroy(w http.ResponseWriter, r *http.Request) {
	// Legacy request format for backward compatibility.
	rs := &struct {
		ID                 string              `json:"id"`
		InstanceID         string              `json:"instance_id"`
		PoolID             string              `json:"pool_id"`
		CorrelationID      string              `json:"correlation_id"`
		InstanceInfo       common.InstanceInfo `json:"instance_info"`
		StorageCleanupType storage.CleanupType `json:"storage_cleanup_type"`
	}{}

	if err := json.NewDecoder(r.Body).Decode(rs); err != nil {
		logrus.WithError(err).Error("could not decode VM destroy request body")
		httprender.BadRequest(w, err.Error(), nil)
		return
	}

	logrus.Infoln("Received destroy request with taskId " + rs.CorrelationID)

	req := &VMCleanupRequest{
		PoolID:             rs.PoolID,
		StageRuntimeID:     rs.ID,
		InstanceInfo:       rs.InstanceInfo,
		StorageCleanupType: rs.StorageCleanupType,
	}
	req.Context.TaskID = rs.CorrelationID

	err := h.service.Destroy(r.Context(), req)
	if err != nil {
		logrus.WithField("stage_runtime_id", req.StageRuntimeID).
			WithField("task_id", rs.CorrelationID).
			WithError(err).Error("could not destroy VM")
		writeError(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// writeError writes an appropriate HTTP error response based on the error type.
func writeError(w http.ResponseWriter, err error) {
	switch err.(type) {
	case *errors.BadRequestError:
		httphelper.WriteBadRequest(w, err)
	case *errors.NotFoundError:
		httphelper.WriteNotFound(w, err)
	default:
		httphelper.WriteInternalError(w, err)
	}
}
