// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

package ds_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/YakDriver/regexache"
	"github.com/YakDriver/smarterr"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/directoryservicedata"
	dsdatatypes "github.com/aws/aws-sdk-go-v2/service/directoryservicedata/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
	"github.com/hashicorp/terraform-provider-aws/internal/acctest"
	"github.com/hashicorp/terraform-provider-aws/internal/flex"
	tfds "github.com/hashicorp/terraform-provider-aws/internal/service/ds"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
	"github.com/hashicorp/terraform-provider-aws/names"
)

func TestUserPasswordImportIDParse(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		id      string
		want    map[string]any
		wantErr bool
	}{
		"valid": {
			id: "d-1234567890,jane.doe",
			want: map[string]any{
				"directory_id": "d-1234567890",
				"user_name":    "jane.doe",
			},
		},
		"no separator": {
			id:      "d-1234567890",
			wantErr: true,
		},
		"empty user": {
			id:      "d-1234567890,",
			wantErr: true,
		},
		"empty directory": {
			id:      ",jane.doe",
			wantErr: true,
		},
		"too many parts": {
			id:      "d-1234567890,jane.doe,extra",
			wantErr: true,
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, got, err := tfds.UserPasswordImportID{}.Parse(testCase.id)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if !maps.Equal(got, testCase.want) {
				t.Errorf("Parse(%q) = %v, want %v", testCase.id, got, testCase.want)
			}
		})
	}
}

func TestUserReplaced(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		stateSID, currentSID string
		want                 bool
	}{
		"same SID":           {"S-1-5-21-1-1-1-1101", "S-1-5-21-1-1-1-1101", false},
		"different SID":      {"S-1-5-21-1-1-1-1101", "S-1-5-21-1-1-1-1102", true},
		"no SID in state":    {"", "S-1-5-21-1-1-1-1101", false},
		"no SID from lookup": {"S-1-5-21-1-1-1-1101", "", false},
		"neither":            {"", "", false},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tfds.UserReplaced(testCase.stateSID, testCase.currentSID); got != testCase.want {
				t.Errorf("UserReplaced(%q, %q) = %t, want %t", testCase.stateSID, testCase.currentSID, got, testCase.want)
			}
		})
	}
}

func TestAccDSUserPassword_basic(t *testing.T) {
	ctx := acctest.Context(t)
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	rName := acctest.RandomWithPrefix(t, acctest.ResourcePrefix)
	domainName := acctest.RandomDomainName(t)
	userName := testAccUserPasswordUserName(t)
	resourceName := "aws_directory_service_user_password.test"
	userResourceName := "aws_directoryservicedata_user.test"

	acctest.ParallelTest(ctx, t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		PreCheck: func() {
			acctest.PreCheck(ctx, t)
			acctest.PreCheckDirectoryService(ctx, t)
		},
		ErrorCheck:               acctest.ErrorCheck(t, names.DSServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		CheckDestroy:             acctest.CheckDestroyNoop,
		Steps: []resource.TestStep{
			{
				Config: testAccUserPasswordConfig_basic(rName, domainName, userName, testAccUserPasswordValue(t), 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckUserPasswordExists(ctx, t, resourceName),
					testAccCheckUserEnabled(ctx, t, userResourceName, true),
					resource.TestCheckResourceAttrPair(resourceName, "directory_id", "aws_directory_service_directory.test", names.AttrID),
					resource.TestCheckResourceAttr(resourceName, names.AttrUserName, userName),
					resource.TestCheckResourceAttr(resourceName, "password_wo_version", "1"),
					resource.TestCheckNoResourceAttr(resourceName, "password_wo"),
					resource.TestCheckResourceAttrPair(resourceName, "user_sid", userResourceName, "sid"),
				),
			},
			{
				// The user resource read its state before the password enabled the user;
				// a refresh must now see it enabled and plan nothing.
				RefreshState: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(userResourceName, names.AttrEnabled, acctest.CtTrue),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateIdFunc:                    acctest.AttrsImportStateIdFunc(resourceName, flex.ResourceIdSeparator, "directory_id", names.AttrUserName),
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "directory_id",
				ImportStateVerifyIgnore:              []string{"password_wo", "password_wo_version", names.AttrTimeouts},
			},
		},
	})
}

// Bumping password_wo_version sets the password again in place. Changing only
// password_wo can't be detected (it's never stored), so it plans nothing.
func TestAccDSUserPassword_rotate(t *testing.T) {
	ctx := acctest.Context(t)
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	rName := acctest.RandomWithPrefix(t, acctest.ResourcePrefix)
	domainName := acctest.RandomDomainName(t)
	userName := testAccUserPasswordUserName(t)
	resourceName := "aws_directory_service_user_password.test"
	firstPassword, secondPassword, thirdPassword := testAccUserPasswordValue(t), testAccUserPasswordValue(t), testAccUserPasswordValue(t)

	acctest.ParallelTest(ctx, t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		PreCheck: func() {
			acctest.PreCheck(ctx, t)
			acctest.PreCheckDirectoryService(ctx, t)
		},
		ErrorCheck:               acctest.ErrorCheck(t, names.DSServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		CheckDestroy:             acctest.CheckDestroyNoop,
		Steps: []resource.TestStep{
			{
				Config: testAccUserPasswordConfig_basic(rName, domainName, userName, firstPassword, 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckUserPasswordExists(ctx, t, resourceName),
					resource.TestCheckResourceAttr(resourceName, "password_wo_version", "1"),
				),
			},
			{
				Config: testAccUserPasswordConfig_basic(rName, domainName, userName, secondPassword, 2),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckUserPasswordExists(ctx, t, resourceName),
					testAccCheckUserEnabled(ctx, t, "aws_directoryservicedata_user.test", true),
					resource.TestCheckResourceAttr(resourceName, "password_wo_version", "2"),
				),
			},
			{
				Config: testAccUserPasswordConfig_basic(rName, domainName, userName, thirdPassword, 2),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

// A user deleted outside Terraform takes the password resource with it.
func TestAccDSUserPassword_disappears_user(t *testing.T) {
	ctx := acctest.Context(t)
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	rName := acctest.RandomWithPrefix(t, acctest.ResourcePrefix)
	domainName := acctest.RandomDomainName(t)
	userName := testAccUserPasswordUserName(t)
	resourceName := "aws_directory_service_user_password.test"
	userResourceName := "aws_directoryservicedata_user.test"

	acctest.ParallelTest(ctx, t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		PreCheck: func() {
			acctest.PreCheck(ctx, t)
			acctest.PreCheckDirectoryService(ctx, t)
		},
		ErrorCheck:               acctest.ErrorCheck(t, names.DSServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		CheckDestroy:             acctest.CheckDestroyNoop,
		Steps: []resource.TestStep{
			{
				Config: testAccUserPasswordConfig_basic(rName, domainName, userName, testAccUserPasswordValue(t), 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckUserPasswordExists(ctx, t, resourceName),
					testAccCheckUserDeleted(ctx, t, userResourceName),
				),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(userResourceName, plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

// A user deleted and created again under the same name outside Terraform has a
// new SID and starts disabled. The user resource can't tell (same name), so the
// password resource must notice the new SID and plan to set the password again.
func TestAccDSUserPassword_userReplaced(t *testing.T) {
	ctx := acctest.Context(t)
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	rName := acctest.RandomWithPrefix(t, acctest.ResourcePrefix)
	domainName := acctest.RandomDomainName(t)
	userName := testAccUserPasswordUserName(t)
	resourceName := "aws_directory_service_user_password.test"
	userResourceName := "aws_directoryservicedata_user.test"
	password := testAccUserPasswordValue(t)

	acctest.ParallelTest(ctx, t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		PreCheck: func() {
			acctest.PreCheck(ctx, t)
			acctest.PreCheckDirectoryService(ctx, t)
		},
		ErrorCheck:               acctest.ErrorCheck(t, names.DSServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		CheckDestroy:             acctest.CheckDestroyNoop,
		Steps: []resource.TestStep{
			{
				Config: testAccUserPasswordConfig_basic(rName, domainName, userName, password, 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckUserPasswordExists(ctx, t, resourceName),
					testAccCheckUserRecreated(ctx, t, userResourceName),
				),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
			},
			{
				Config: testAccUserPasswordConfig_basic(rName, domainName, userName, password, 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckUserEnabled(ctx, t, userResourceName, true),
					resource.TestCheckResourceAttrPair(resourceName, "user_sid", userResourceName, "sid"),
				),
			},
		},
	})
}

func TestAccDSUserPassword_invalidPassword(t *testing.T) {
	ctx := acctest.Context(t)
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	rName := acctest.RandomWithPrefix(t, acctest.ResourcePrefix)
	domainName := acctest.RandomDomainName(t)
	userName := testAccUserPasswordUserName(t)

	acctest.ParallelTest(ctx, t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_11_0),
		},
		PreCheck: func() {
			acctest.PreCheck(ctx, t)
			acctest.PreCheckDirectoryService(ctx, t)
		},
		ErrorCheck:               acctest.ErrorCheck(t, names.DSServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		CheckDestroy:             acctest.CheckDestroyNoop,
		Steps: []resource.TestStep{
			{
				// Too short and too simple for the directory's default complexity rules.
				Config:      testAccUserPasswordConfig_basic(rName, domainName, userName, "abc", 1),
				ExpectError: regexache.MustCompile(`InvalidPasswordException`),
			},
		},
	})
}

func testAccUserPasswordUserName(t *testing.T) string {
	t.Helper()

	// sAMAccountName is limited to 20 characters for users.
	return fmt.Sprintf(
		"%s%s",
		acctest.ResourcePrefix,
		acctest.RandStringFromCharSet(t, 20-len(acctest.ResourcePrefix), "abcdefghijklmnopqrstuvwxyz0123456789"),
	)
}

// testAccUserPasswordValue returns a random password that meets the default
// Active Directory complexity rules (upper, lower, digit, symbol; not based on
// the user name).
func testAccUserPasswordValue(t *testing.T) string {
	t.Helper()

	return fmt.Sprintf("Tf-%s-9x", acctest.RandStringFromCharSet(t, 16, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"))
}

func testAccCheckUserPasswordExists(ctx context.Context, t *testing.T, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return smarterr.NewError(errors.New("not found"))
		}

		conn := acctest.ProviderMeta(ctx, t).DirectoryServiceDataClient(ctx)
		_, err := tfds.FindUserSIDByTwoPartKey(ctx, conn, rs.Primary.Attributes["directory_id"], rs.Primary.Attributes[names.AttrUserName])

		return smarterr.NewError(err)
	}
}

// testAccCheckUserEnabled reads the user from the API rather than from state,
// because the user resource's own state is read before the password is set.
func testAccCheckUserEnabled(ctx context.Context, t *testing.T, name string, want bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return smarterr.NewError(errors.New("not found"))
		}

		conn := acctest.ProviderMeta(ctx, t).DirectoryServiceDataClient(ctx)
		directoryID, userName := rs.Primary.Attributes["directory_id"], rs.Primary.Attributes["sam_account_name"]

		// The domain controller that answers may not have the change yet.
		_, err := tfresource.RetryUntilEqual(ctx, 2*time.Minute, want, func(ctx context.Context) (bool, error) {
			output, err := conn.DescribeUser(ctx, &directoryservicedata.DescribeUserInput{DirectoryId: aws.String(directoryID), SAMAccountName: aws.String(userName)})
			if err != nil {
				return false, err
			}
			return aws.ToBool(output.Enabled), nil
		})

		return smarterr.NewError(err)
	}
}

// testAccCheckUserDeleted deletes the user through the API, outside Terraform.
func testAccCheckUserDeleted(ctx context.Context, t *testing.T, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return smarterr.NewError(errors.New("not found"))
		}

		conn := acctest.ProviderMeta(ctx, t).DirectoryServiceDataClient(ctx)
		_, err := conn.DeleteUser(ctx, &directoryservicedata.DeleteUserInput{
			DirectoryId:    aws.String(rs.Primary.Attributes["directory_id"]),
			SAMAccountName: aws.String(rs.Primary.Attributes["sam_account_name"]),
		})

		return smarterr.NewError(err)
	}
}

// testAccCheckUserRecreated deletes the user and creates it again under the same
// name through the API, which gives it a new SID.
func testAccCheckUserRecreated(ctx context.Context, t *testing.T, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return smarterr.NewError(errors.New("not found"))
		}

		conn := acctest.ProviderMeta(ctx, t).DirectoryServiceDataClient(ctx)
		directoryID, userName := aws.String(rs.Primary.Attributes["directory_id"]), aws.String(rs.Primary.Attributes["sam_account_name"])

		if _, err := conn.DeleteUser(ctx, &directoryservicedata.DeleteUserInput{DirectoryId: directoryID, SAMAccountName: userName}); err != nil {
			return smarterr.NewError(err)
		}

		// Creating the same name straight after deleting it can conflict until the
		// deletion replicates.
		_, err := tfresource.RetryWhenIsA[*directoryservicedata.CreateUserOutput, *dsdatatypes.ConflictException](ctx, 2*time.Minute, func(ctx context.Context) (*directoryservicedata.CreateUserOutput, error) {
			return conn.CreateUser(ctx, &directoryservicedata.CreateUserInput{DirectoryId: directoryID, SAMAccountName: userName})
		})

		return smarterr.NewError(err)
	}
}

func testAccUserPasswordConfig_basic(rName, domainName, userName, password string, version int) string {
	return acctest.ConfigCompose(
		acctest.ConfigVPCWithSubnets(rName, 2),
		fmt.Sprintf(`
resource "aws_directory_service_directory" "test" {
  name                         = %[1]q
  password                     = "SuperSecretPassw0rd"
  type                         = "MicrosoftAD"
  edition                      = "Standard"
  enable_directory_data_access = true

  vpc_settings {
    vpc_id     = aws_vpc.test.id
    subnet_ids = aws_subnet.test[*].id
  }
}

resource "aws_directoryservicedata_user" "test" {
  directory_id     = aws_directory_service_directory.test.id
  sam_account_name = %[2]q
}

resource "aws_directory_service_user_password" "test" {
  directory_id        = aws_directory_service_directory.test.id
  user_name           = aws_directoryservicedata_user.test.sam_account_name
  password_wo         = %[3]q
  password_wo_version = %[4]d
}
`, domainName, userName, password, version),
	)
}
