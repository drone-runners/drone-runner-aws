// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

// Package userdata contains code to generate userdata scripts executed by the cloud-init directive.

package httprender

import (
	"encoding/json"
	"net/http"
)

func OK(w http.ResponseWriter, v interface{}) {
	JSON(w, v, http.StatusOK)
}

func JSON(w http.ResponseWriter, v interface{}, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func BadRequest(w http.ResponseWriter, message string) {
	clientError(w, message, http.StatusBadRequest)
}

func NotFound(w http.ResponseWriter, message string) {
	clientError(w, message, http.StatusNotFound)
}

func clientError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(message))
}

func InternalError(w http.ResponseWriter) {
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}
