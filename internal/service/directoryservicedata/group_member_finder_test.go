// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

package directoryservicedata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/directoryservicedata"
	"github.com/hashicorp/terraform-provider-aws/internal/retry"
)

// listGroupMembersRequest records what the client sent, so tests can assert on
// the query string and body as well as the result.
type listGroupMembersRequest struct {
	path        string
	directoryID string
	body        map[string]any
}

// newTestClient returns a Directory Service Data client that sends every
// request to handler on a local httptest server.
func newTestClient(t *testing.T, handler http.HandlerFunc) *directoryservicedata.Client {
	t.Helper()

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	return directoryservicedata.New(directoryservicedata.Options{
		Region:       "us-west-2", //lintignore:AWSAT003
		BaseEndpoint: aws.String(ts.URL),
		Credentials:  credentials.NewStaticCredentialsProvider("AKID", "SECRET", "SESSION"),
	})
}

// listGroupMembersHandler serves pages in order, following NextToken, and
// records each request.
func listGroupMembersHandler(t *testing.T, pages []map[string]any, requests *[]listGroupMembersRequest, mu *sync.Mutex) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request body: %s", err)
		}

		mu.Lock()
		*requests = append(*requests, listGroupMembersRequest{path: r.URL.Path, directoryID: r.URL.Query().Get("DirectoryId"), body: body})
		mu.Unlock()

		page := 0
		if token, ok := body["NextToken"].(string); ok {
			for i := range pages {
				if token == pageToken(i) {
					page = i
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(pages[page]) //nolint:errcheck // just testing
	}
}

func pageToken(i int) string {
	return "token-" + string(rune('a'+i))
}

func errorHandler(statusCode int, errorType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Amzn-ErrorType", errorType)
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(map[string]any{"message": "test error"}) //nolint:errcheck // just testing
	}
}

func TestFindGroupMemberByFourPartKey_paginates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pages := []map[string]any{
		{
			"DirectoryId": "d-1234567890",
			"Members": []map[string]any{
				{"MemberType": "USER", "SAMAccountName": "john.doe", "SID": "S-1-5-21-1-1-1-1101"},
			},
			"NextToken": pageToken(1),
		},
		{
			"DirectoryId": "d-1234567890",
			"Members": []map[string]any{
				{"MemberType": "USER", "SAMAccountName": "Jane.Doe", "SID": "S-1-5-21-1-1-1-1102"},
			},
		},
	}

	var requests []listGroupMembersRequest
	var mu sync.Mutex
	conn := newTestClient(t, listGroupMembersHandler(t, pages, &requests, &mu))

	member, err := findGroupMemberByFourPartKey(ctx, conn, "d-1234567890", "app-admins", "jane.doe", "")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got, want := aws.ToString(member.SID), "S-1-5-21-1-1-1-1102"; got != want {
		t.Errorf("SID = %q, want %q", got, want)
	}

	if len(requests) != 2 {
		t.Fatalf("expected 2 requests (one per page), got %d", len(requests))
	}
	first := requests[0]
	if first.path != "/GroupMemberships/ListGroupMembers" {
		t.Errorf("path = %q, want /GroupMemberships/ListGroupMembers", first.path)
	}
	if first.directoryID != "d-1234567890" {
		t.Errorf("DirectoryId = %q, want d-1234567890", first.directoryID)
	}
	if got := first.body["SAMAccountName"]; got != "app-admins" {
		t.Errorf("SAMAccountName = %v, want app-admins", got)
	}
	if _, ok := first.body["MemberRealm"]; ok {
		t.Errorf("MemberRealm sent although none was configured: %v", first.body["MemberRealm"])
	}
}

func TestFindGroupMemberByFourPartKey_sendsMemberRealm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pages := []map[string]any{
		{
			"DirectoryId": "d-1234567890",
			"MemberRealm": "trusted.example.com",
			"Members": []map[string]any{
				{"MemberType": "USER", "SAMAccountName": "jane.doe", "SID": "S-1-5-21-2-2-2-1102"},
			},
		},
	}

	var requests []listGroupMembersRequest
	var mu sync.Mutex
	conn := newTestClient(t, listGroupMembersHandler(t, pages, &requests, &mu))

	if _, err := findGroupMemberByFourPartKey(ctx, conn, "d-1234567890", "app-admins", "jane.doe", "trusted.example.com"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got := requests[0].body["MemberRealm"]; got != "trusted.example.com" {
		t.Errorf("MemberRealm = %v, want trusted.example.com", got)
	}
}

func TestFindGroupMemberByFourPartKey_memberMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pages := []map[string]any{
		{
			"DirectoryId": "d-1234567890",
			"Members": []map[string]any{
				{"MemberType": "USER", "SAMAccountName": "john.doe", "SID": "S-1-5-21-1-1-1-1101"},
			},
		},
	}

	var requests []listGroupMembersRequest
	var mu sync.Mutex
	conn := newTestClient(t, listGroupMembersHandler(t, pages, &requests, &mu))

	_, err := findGroupMemberByFourPartKey(ctx, conn, "d-1234567890", "app-admins", "jane.doe", "")
	if !retry.NotFound(err) {
		t.Fatalf("expected a not-found error, got: %v", err)
	}
}

func TestFindGroupMemberByFourPartKey_groupMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conn := newTestClient(t, errorHandler(http.StatusNotFound, "ResourceNotFoundException"))

	_, err := findGroupMemberByFourPartKey(ctx, conn, "d-1234567890", "app-admins", "jane.doe", "")
	if !retry.NotFound(err) {
		t.Fatalf("expected a not-found error, got: %v", err)
	}
}

func TestFindGroupMemberByFourPartKey_accessDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conn := newTestClient(t, errorHandler(http.StatusForbidden, "AccessDeniedException"))

	_, err := findGroupMemberByFourPartKey(ctx, conn, "d-1234567890", "app-admins", "jane.doe", "")
	if err == nil || retry.NotFound(err) {
		t.Fatalf("expected access denied to be returned as an error, not as not found; got: %v", err)
	}
}
