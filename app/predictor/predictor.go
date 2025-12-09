package predictor

import (
	"context"
)

// PredictionInput contains the input data for making a prediction.
type PredictionInput struct {
	// PoolName is the name of the pool to predict for.
	PoolName       string
	StartTimestamp int64
	EndTimestamp   int64
	VariantID      string
}

// PredictionResult contains the output of a prediction.
type PredictionResult struct {
	// RecommendedInstances is the recommended number of instances to have available.
	RecommendedInstances int
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
