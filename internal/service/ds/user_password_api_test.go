// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

package ds

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/directoryservice"
	"github.com/aws/aws-sdk-go-v2/service/directoryservicedata"
	"github.com/hashicorp/terraform-provider-aws/internal/retry"
)

// These tests run the API helpers of aws_directory_service_user_password
// against a local httptest server, so they need no AWS account.

const testPassword = "Example-not-secret-1" // #nosec G101 -- test placeholder, not a credential

type recordedRequest struct {
	target string
	path   string
	query  string
	body   map[string]any
}

type recorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (r *recorder) record(t *testing.T, req *http.Request) {
	t.Helper()

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Errorf("decoding request body: %s", err)
	}
	v := recordedRequest{target: req.Header.Get("X-Amz-Target"), path: req.URL.Path, query: req.URL.RawQuery, body: body}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, v)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.requests)
}

func newTestServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	return ts.URL
}

func newTestDSClient(t *testing.T, handler http.HandlerFunc) *directoryservice.Client {
	t.Helper()

	return directoryservice.New(directoryservice.Options{
		Region:       "us-west-2", //lintignore:AWSAT003
		BaseEndpoint: aws.String(newTestServer(t, handler)),
		Credentials:  credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION"),
	})
}

func newTestDSDataClient(t *testing.T, handler http.HandlerFunc) *directoryservicedata.Client {
	t.Helper()

	return directoryservicedata.New(directoryservicedata.Options{
		Region:       "us-west-2", //lintignore:AWSAT003
		BaseEndpoint: aws.String(newTestServer(t, handler)),
		Credentials:  credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION"),
	})
}

// writeJSON11Error writes an error the way the Directory Service (awsJson1_1) API does.
func writeJSON11Error(w http.ResponseWriter, errorType, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]any{"__type": errorType, "Message": message}) //nolint:errcheck // just testing
}

// writeRESTJSONError writes an error the way the Directory Service Data (restJson1) API does.
func writeRESTJSONError(w http.ResponseWriter, statusCode int, errorType string, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Amzn-ErrorType", errorType)
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(body) //nolint:errcheck // just testing
}

func TestResetUserPassword_sendsRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var rec recorder
	conn := newTestDSClient(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(t, r)
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.Write([]byte(`{}`)) //nolint:errcheck // just testing
	})

	if err := resetUserPassword(ctx, conn, "d-1234567890", "jane.doe", testPassword, time.Minute); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if rec.count() != 1 {
		t.Fatalf("expected 1 request, got %d", rec.count())
	}
	got := rec.requests[0]
	if got.target != "DirectoryService_20150416.ResetUserPassword" {
		t.Errorf("X-Amz-Target = %q, want DirectoryService_20150416.ResetUserPassword", got.target)
	}
	for key, want := range map[string]string{"DirectoryId": "d-1234567890", "UserName": "jane.doe", "NewPassword": testPassword} {
		if got.body[key] != want {
			t.Errorf("%s = %v, want %q", key, got.body[key], want)
		}
	}
}

// A user created moments earlier may not be on the domain controller that
// serves the reset yet; the helper must retry until it is.
func TestResetUserPassword_retriesUntilUserExists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var rec recorder
	conn := newTestDSClient(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(t, r)
		if rec.count() < 3 {
			writeJSON11Error(w, "UserDoesNotExistException", "user not found")
			return
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.Write([]byte(`{}`)) //nolint:errcheck // just testing
	})

	if err := resetUserPassword(ctx, conn, "d-1234567890", "jane.doe", testPassword, time.Minute); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if rec.count() < 3 {
		t.Errorf("expected at least 3 requests (2 retried), got %d", rec.count())
	}
}

// A password the directory rejects must fail at once, and the error must not
// contain the password.
func TestResetUserPassword_invalidPasswordNotRetried(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var rec recorder
	conn := newTestDSClient(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(t, r)
		writeJSON11Error(w, "InvalidPasswordException", "The new password provided by the user does not meet the password complexity requirements defined in your directory.")
	})

	err := resetUserPassword(ctx, conn, "d-1234567890", "jane.doe", testPassword, time.Minute)
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	if !strings.Contains(err.Error(), "InvalidPasswordException") {
		t.Errorf("error %q does not name InvalidPasswordException", err)
	}
	if strings.Contains(err.Error(), testPassword) {
		t.Errorf("error contains the password: %q", err)
	}
	if rec.count() != 1 {
		t.Errorf("expected 1 request (no retry), got %d", rec.count())
	}
}

func TestFindUserSIDByTwoPartKey(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		handler          http.HandlerFunc
		wantSID          string
		wantNotFound     bool
		wantDataDisabled bool
		wantErr          bool
	}{
		"found": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // just testing
					"DirectoryId":    "d-1234567890",
					"SAMAccountName": "jane.doe",
					"SID":            "S-1-5-21-1-1-1-1102",
					"Enabled":        true,
				})
			},
			wantSID: "S-1-5-21-1-1-1-1102",
		},
		"user missing": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeRESTJSONError(w, http.StatusNotFound, "ResourceNotFoundException", map[string]any{"Message": "not found"})
			},
			wantNotFound: true,
			wantErr:      true,
		},
		"no SID in response": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"DirectoryId":"d-1234567890"}`)) //nolint:errcheck // just testing
			},
			wantNotFound: true,
			wantErr:      true,
		},
		"data access disabled": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeRESTJSONError(w, http.StatusForbidden, "AccessDeniedException", map[string]any{"Message": "disabled", "Reason": "DATA_DISABLED"})
			},
			wantDataDisabled: true,
			wantErr:          true,
		},
		"IAM denied": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeRESTJSONError(w, http.StatusForbidden, "AccessDeniedException", map[string]any{"Message": "denied", "Reason": "IAM_AUTH"})
			},
			wantErr: true,
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			sid, err := findUserSIDByTwoPartKey(ctx, newTestDSDataClient(t, testCase.handler), "d-1234567890", "jane.doe")

			if got := err != nil; got != testCase.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, testCase.wantErr)
			}
			if got := retry.NotFound(err); got != testCase.wantNotFound {
				t.Errorf("NotFound(%v) = %t, want %t", err, got, testCase.wantNotFound)
			}
			if got := isDirectoryDataAccessDisabled(err); got != testCase.wantDataDisabled {
				t.Errorf("isDirectoryDataAccessDisabled(%v) = %t, want %t", err, got, testCase.wantDataDisabled)
			}
			if sid != testCase.wantSID {
				t.Errorf("SID = %q, want %q", sid, testCase.wantSID)
			}
		})
	}
}

func TestFindUserSIDByTwoPartKey_sendsRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var rec recorder
	conn := newTestDSDataClient(t, func(w http.ResponseWriter, r *http.Request) {
		rec.record(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"SID":"S-1-5-21-1-1-1-1102"}`)) //nolint:errcheck // just testing
	})

	if _, err := findUserSIDByTwoPartKey(ctx, conn, "d-1234567890", "jane.doe"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	got := rec.requests[0]
	if got.path != "/Users/DescribeUser" {
		t.Errorf("path = %q, want /Users/DescribeUser", got.path)
	}
	if got.query != "DirectoryId=d-1234567890" {
		t.Errorf("query = %q, want DirectoryId=d-1234567890", got.query)
	}
	if got.body["SAMAccountName"] != "jane.doe" {
		t.Errorf("SAMAccountName = %v, want jane.doe", got.body["SAMAccountName"])
	}
}
