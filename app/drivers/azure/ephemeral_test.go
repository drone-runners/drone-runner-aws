package azure

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
		{name: "404", err: &azcore.ResponseError{StatusCode: http.StatusNotFound}, want: true},
		{name: "wrapped 404", err: fmt.Errorf("delete: %w", &azcore.ResponseError{StatusCode: http.StatusNotFound}), want: true},
		{name: "500", err: &azcore.ResponseError{StatusCode: http.StatusInternalServerError}, want: false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := isNotFound(tc.err); got != tc.want {
				t.Fatalf("isNotFound() = %v, want %v", got, tc.want)
			}
		})
	}
}
