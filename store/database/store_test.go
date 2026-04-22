package database

import (
	"testing"
)

func Test_parseDSN(t *testing.T) {
	tests := []struct {
		name        string
		dsn         string
		wantHost    string
		wantPort    string
		wantUser    string
		wantDBName  string
		wantSSLMode string
		wantErr     bool
	}{
		{
			name:        "all fields present",
			dsn:         "host=mydb.example.com port=5432 user=admin1 dbname=dlite sslmode=require",
			wantHost:    "mydb.example.com",
			wantPort:    "5432",
			wantUser:    "admin1",
			wantDBName:  "dlite",
			wantSSLMode: "require",
		},
		{
			name:        "port defaults to 5432 when absent",
			dsn:         "host=mydb.example.com user=admin1 dbname=dlite sslmode=require",
			wantHost:    "mydb.example.com",
			wantPort:    "5432",
			wantUser:    "admin1",
			wantDBName:  "dlite",
			wantSSLMode: "require",
		},
		{
			name:        "sslmode absent",
			dsn:         "host=mydb.example.com port=5433 user=admin1 dbname=dlite",
			wantHost:    "mydb.example.com",
			wantPort:    "5433",
			wantUser:    "admin1",
			wantDBName:  "dlite",
			wantSSLMode: "",
		},
		{
			name:    "missing host returns error",
			dsn:     "user=admin1 dbname=dlite sslmode=require",
			wantErr: true,
		},
		{
			name:    "missing user returns error",
			dsn:     "host=mydb.example.com dbname=dlite sslmode=require",
			wantErr: true,
		},
		{
			name:    "empty string returns error",
			dsn:     "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host, port, user, dbname, sslmode, err := parseDSN(tc.dsn)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseDSN() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if host != tc.wantHost {
				t.Errorf("host = %q, want %q", host, tc.wantHost)
			}
			if port != tc.wantPort {
				t.Errorf("port = %q, want %q", port, tc.wantPort)
			}
			if user != tc.wantUser {
				t.Errorf("user = %q, want %q", user, tc.wantUser)
			}
			if dbname != tc.wantDBName {
				t.Errorf("dbname = %q, want %q", dbname, tc.wantDBName)
			}
			if sslmode != tc.wantSSLMode {
				t.Errorf("sslmode = %q, want %q", sslmode, tc.wantSSLMode)
			}
		})
	}
}
