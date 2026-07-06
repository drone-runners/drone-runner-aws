package predictor

import (
	"context"
)

// PredictionInput contains the input data for making a prediction.
type PredictionInput struct {
	// PoolName is the name of the pool to predict for.
	PoolName string
	// TenantID scopes the prediction to a single tenant of the pool. Empty means the default tenant.
	TenantID       string
	StartTimestamp int64
	EndTimestamp   int64
	VariantID      string
	ImageName      string
}

// PredictionResult contains the output of a prediction.
type PredictionResult struct {
	// PredictedInstances is the recommended base instance count derived from
	// historical/EMA analysis. MinInstances floors this value. Callers (the scaler)
	// are responsible for any additional over-provisioning buffer — the predictor
	// no longer applies ScalePercent.
	PredictedInstances int
}

// Predictor defines the interface for predicting the number of machines required.
type Predictor interface {
	// Predict predicts the number of machines required based on the given input.
	// It returns a PredictionResult containing the recommended number of instances
	// and related metadata.
	Predict(ctx context.Context, input *PredictionInput) (*PredictionResult, error)

	// Name returns the name of the predictor implementation.
	Name() string
}
