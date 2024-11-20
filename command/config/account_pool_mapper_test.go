package config

import (
	"reflect"
	"testing"
)

func TestPoolMapperByAccount_Convert(t *testing.T) {
	tests := []struct {
		name string
		pma  PoolMapperByAccount
		want map[string]map[string]string
	}{
		{
			name: "empty mapper",
			pma:  PoolMapperByAccount{},
			want: map[string]map[string]string{},
		},
		{
			name: "single account with empty pool map",
			pma: PoolMapperByAccount{
				"account1": PoolMap{},
			},
			want: map[string]map[string]string{
				"account1": {},
			},
		},
		{
			name: "single account with multiple pools",
			pma: PoolMapperByAccount{
				"account1": PoolMap{
					"pool1": "value1",
					"pool2": "value2",
				},
			},
			want: map[string]map[string]string{
				"account1": {
					"pool1": "value1",
					"pool2": "value2",
				},
			},
		},
		{
			name: "multiple accounts with multiple pools",
			pma: PoolMapperByAccount{
				"account1": PoolMap{
					"pool1": "value1",
					"pool2": "value2",
				},
				"account2": PoolMap{
					"pool3": "value3",
					"pool4": "value4",
				},
			},
			want: map[string]map[string]string{
				"account1": {
					"pool1": "value1",
					"pool2": "value2",
				},
				"account2": {
					"pool3": "value3",
					"pool4": "value4",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.pma.Convert()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PoolMapperByAccount.Convert() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPoolMapperByAccount_Decode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    PoolMapperByAccount
		wantErr bool
	}{
		{
			name:    "empty input",
			input:   "",
			want:    PoolMapperByAccount{},
			wantErr: true,
		},
		{
			name:  "single account with empty pool",
			input: `account1={}`,
			want: PoolMapperByAccount{
				"account1": PoolMap{},
			},
		},
		{
			name:  "single account with pools",
			input: `account1={"pool1":"value1","pool2":"value2"}`,
			want: PoolMapperByAccount{
				"account1": PoolMap{
					"pool1": "value1",
					"pool2": "value2",
				},
			},
		},
		{
			name:  "multiple accounts with pools",
			input: `account1={"pool1":"value1","pool2":"value2"};account2={"pool3":"value3","pool4":"value4"}`,
			want: PoolMapperByAccount{
				"account1": PoolMap{
					"pool1": "value1",
					"pool2": "value2",
				},
				"account2": PoolMap{
					"pool3": "value3",
					"pool4": "value4",
				},
			},
		},
		{
			name:    "invalid format - missing value",
			input:   "account1",
			wantErr: true,
		},
		{
			name:    "invalid format - wrong separator",
			input:   "account1:{}",
			wantErr: true,
		},
		{
			name:    "invalid json",
			input:   "account1={invalid_json}",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pma PoolMapperByAccount
			err := pma.Decode(tt.input)

			// Check error cases
			if (err != nil) != tt.wantErr {
				t.Errorf("PoolMapperByAccount.Decode() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Skip further checks if we expected an error
			if tt.wantErr {
				return
			}

			// Check the decoded result
			if !reflect.DeepEqual(pma, tt.want) {
				t.Errorf("PoolMapperByAccount.Decode() = %v, want %v", pma, tt.want)
			}

			// Verify that Convert works on decoded data
			converted := pma.Convert()
			expectedConverted := tt.want.Convert()
			if !reflect.DeepEqual(converted, expectedConverted) {
				t.Errorf("PoolMapperByAccount.Convert() after Decode = %v, want %v", converted, expectedConverted)
			}
		})
	}
}
