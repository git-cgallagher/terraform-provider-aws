// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

package directoryservicedata

import (
	"maps"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awstypes "github.com/aws/aws-sdk-go-v2/service/directoryservicedata/types"
)

func TestGroupMemberImportIDParse(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		id       string
		expected map[string]any
		wantErr  bool
	}{
		"three parts": {
			id: "d-1234567890,app-admins,jane.doe",
			expected: map[string]any{
				"directory_id": "d-1234567890",
				"group_name":   "app-admins",
				"member_name":  "jane.doe",
			},
		},
		"four parts": {
			id: "d-1234567890,app-admins,jane.doe,trusted.example.com",
			expected: map[string]any{
				"directory_id": "d-1234567890",
				"group_name":   "app-admins",
				"member_name":  "jane.doe",
				"member_realm": "trusted.example.com",
			},
		},
		"two parts":  {id: "d-1234567890,app-admins", wantErr: true},
		"five parts": {id: "d-1234567890,app-admins,jane.doe,example.com,extra", wantErr: true},
		"empty part": {id: "d-1234567890,,jane.doe", wantErr: true},
		"empty":      {id: "", wantErr: true},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			id, got, err := groupMemberImportID{}.Parse(testCase.id)

			if testCase.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got none", testCase.id)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if id != testCase.id {
				t.Errorf("ID = %q, want %q", id, testCase.id)
			}
			if !maps.Equal(got, testCase.expected) {
				t.Errorf("attributes = %v, want %v", got, testCase.expected)
			}
		})
	}
}

func TestGroupMemberResourceID(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		directoryID, groupName, memberName, memberRealm string
		expected                                        string
	}{
		"without realm": {"d-1234567890", "app-admins", "jane.doe", "", "d-1234567890,app-admins,jane.doe"},
		"with realm":    {"d-1234567890", "app-admins", "jane.doe", "trusted.example.com", "d-1234567890,app-admins,jane.doe,trusted.example.com"},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := groupMemberResourceID(testCase.directoryID, testCase.groupName, testCase.memberName, testCase.memberRealm)
			if got != testCase.expected {
				t.Errorf("groupMemberResourceID() = %q, want %q", got, testCase.expected)
			}

			// The ID must parse back to the same parts, so import round-trips.
			if _, _, err := (groupMemberImportID{}).Parse(got); err != nil {
				t.Errorf("Parse(%q): %s", got, err)
			}
		})
	}
}

func TestFindMemberBySAMAccountName(t *testing.T) {
	t.Parallel()

	members := []awstypes.Member{
		{SAMAccountName: aws.String("app-admins"), SID: aws.String("S-1-5-21-1-1-1-1101"), MemberType: awstypes.MemberTypeGroup},
		{SAMAccountName: aws.String("Jane.Doe"), SID: aws.String("S-1-5-21-1-1-1-1102"), MemberType: awstypes.MemberTypeUser},
	}

	testCases := map[string]struct {
		name    string
		wantSID string
	}{
		"exact case":     {name: "app-admins", wantSID: "S-1-5-21-1-1-1-1101"},
		"different case": {name: "jane.doe", wantSID: "S-1-5-21-1-1-1-1102"},
		"upper case":     {name: "JANE.DOE", wantSID: "S-1-5-21-1-1-1-1102"},
		"missing":        {name: "john.doe"},
		"prefix only":    {name: "jane"},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := findMemberBySAMAccountName(members, testCase.name)

			if testCase.wantSID == "" {
				if got != nil {
					t.Fatalf("expected no match for %q, got %q", testCase.name, aws.ToString(got.SAMAccountName))
				}
				return
			}
			if got == nil {
				t.Fatalf("expected a match for %q, got none", testCase.name)
			}
			if aws.ToString(got.SID) != testCase.wantSID {
				t.Errorf("SID = %q, want %q", aws.ToString(got.SID), testCase.wantSID)
			}
		})
	}

	if got := findMemberBySAMAccountName(nil, "jane.doe"); got != nil {
		t.Errorf("expected no match in an empty page, got %v", got)
	}
}
