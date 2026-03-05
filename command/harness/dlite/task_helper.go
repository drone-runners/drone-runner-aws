package dlite

import (
	"encoding/json"
	"net/http"

	"github.com/sirupsen/logrus"
	"github.com/wings-software/dlite/client"
	"github.com/wings-software/dlite/httphelper"
)

// decodeTask decodes a task from the HTTP request body.
// Returns the task, taskBytes (for further unmarshaling), logger, and any error.
func decodeTask(w http.ResponseWriter, r *http.Request) (*client.Task, []byte, *logrus.Entry, bool) {
	log := logrus.New()
	task := &client.Task{}

	if err := json.NewDecoder(r.Body).Decode(task); err != nil {
		log.WithError(err).Error("could not decode task HTTP body")
		httphelper.WriteBadRequest(w, err)
		return nil, nil, nil, false
	}

	logr := log.WithField("task_id", task.ID)

	taskBytes, err := task.Data.MarshalJSON()
	if err != nil {
		logr.WithError(err).Error("could not unmarshal task data")
		httphelper.WriteBadRequest(w, err)
		return nil, nil, nil, false
	}

	return task, taskBytes, logr, true
}

// unmarshalTaskRequest unmarshals task bytes into the provided request struct.
func unmarshalTaskRequest(w http.ResponseWriter, taskBytes []byte, req interface{}, logr *logrus.Entry) bool {
	if err := json.Unmarshal(taskBytes, req); err != nil {
		logr.WithError(err).Error("could not unmarshal task request data")
		httphelper.WriteBadRequest(w, err)
		return false
	}
	return true
}

// writeErrorResponse writes an error response.
func writeErrorResponse(w http.ResponseWriter, err error) {
	httphelper.WriteJSON(w, failedResponse(err.Error()), httpFailed)
}

// writeSuccessResponse writes a success response.
func writeSuccessResponse(w http.ResponseWriter, resp *VMTaskExecutionResponse) {
	httphelper.WriteJSON(w, resp, httpOK)
}
