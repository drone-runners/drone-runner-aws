// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

// Package userdata contains code to generate userdata scripts executed by the cloud-init directive.

package httprender

import (
	"encoding/json"
	"net/http"

	"github.com/sirupsen/logrus"
)

func OK(w http.ResponseWriter, v interface{}) {
	JSON(w, v, http.StatusOK)
}

func JSON(w http.ResponseWriter, v interface{}, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func BadRequest(w http.ResponseWriter, message string, logr *logrus.Entry) {
	ClientError(w, message, http.StatusBadRequest, logr)
}

func NotFound(w http.ResponseWriter, message string, logr *logrus.Entry) {
	ClientError(w, message, http.StatusNotFound, logr)
}

func ClientError(w http.ResponseWriter, message string, status int, logr *logrus.Entry) {
	if message == "" {
		w.WriteHeader(status)
		return
	}

	if logr != nil {
		logr.Debugln(message)
	}

	Error(w, message, status)
}

func Error(w http.ResponseWriter, message string, status int) {
	out := struct {
		Message string `json:"error_msg"`
	}{
		Message: message,
	}

	JSON(w, &out, status)
}

func InternalError(w http.ResponseWriter, message string, err error, logr *logrus.Entry) {
	if err == nil {
		if message == "" {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			if logr != nil {
				logr.Errorln(message)
			}
			Error(w, message, http.StatusInternalServerError)
		}
	} else {
		if message == "" {
			if logr != nil {
				logr.WithError(err).Errorln()
			}
			Error(w, err.Error(), http.StatusInternalServerError)
		} else {
			if logr != nil {
				logr.WithError(err).Errorln(message)
			}
			Error(w, message+": "+err.Error(), http.StatusInternalServerError)
		}
	}
}
