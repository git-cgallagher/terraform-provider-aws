// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

package directoryservicedata_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/YakDriver/regexache"
	"github.com/YakDriver/smarterr"
	awstypes "github.com/aws/aws-sdk-go-v2/service/directoryservicedata/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-provider-aws/internal/acctest"
	"github.com/hashicorp/terraform-provider-aws/internal/errs"
	"github.com/hashicorp/terraform-provider-aws/internal/flex"
	"github.com/hashicorp/terraform-provider-aws/internal/retry"
	tfdirectoryservicedata "github.com/hashicorp/terraform-provider-aws/internal/service/directoryservicedata"
	"github.com/hashicorp/terraform-provider-aws/names"
)

func TestAccDirectoryServiceDataGroupMember_basic(t *testing.T) {
	ctx := acctest.Context(t)
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	rName := acctest.RandomWithPrefix(t, acctest.ResourcePrefix)
	domainName := acctest.RandomDomainName(t)
	groupName, userName := testAccGroupMemberNames(t)
	resourceName := "aws_directoryservicedata_group_member.test"

	acctest.ParallelTest(ctx, t, resource.TestCase{
		PreCheck: func() {
			acctest.PreCheck(ctx, t)
			acctest.PreCheckDirectoryService(ctx, t)
		},
		ErrorCheck:               acctest.ErrorCheck(t, names.DirectoryServiceDataServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		CheckDestroy:             testAccCheckGroupMemberDestroy(ctx, t),
		Steps: []resource.TestStep{
			{
				Config: testAccGroupMemberConfig_basic(rName, domainName, groupName, userName),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckGroupMemberExists(ctx, t, resourceName),
					resource.TestCheckResourceAttrPair(resourceName, "directory_id", "aws_directory_service_directory.test", names.AttrID),
					resource.TestCheckResourceAttr(resourceName, names.AttrGroupName, groupName),
					resource.TestCheckResourceAttr(resourceName, "member_name", userName),
					resource.TestCheckNoResourceAttr(resourceName, "member_realm"),
					resource.TestCheckResourceAttrPair(resourceName, "member_sid", "aws_directoryservicedata_user.test", "sid"),
					resource.TestCheckResourceAttr(resourceName, "member_type", string(awstypes.MemberTypeUser)),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateIdFunc:                    acctest.AttrsImportStateIdFunc(resourceName, flex.ResourceIdSeparator, "directory_id", names.AttrGroupName, "member_name"),
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "directory_id",
				ImportStateVerifyIgnore:              []string{names.AttrTimeouts},
			},
		},
	})
}

func TestAccDirectoryServiceDataGroupMember_disappears(t *testing.T) {
	ctx := acctest.Context(t)
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	rName := acctest.RandomWithPrefix(t, acctest.ResourcePrefix)
	domainName := acctest.RandomDomainName(t)
	groupName, userName := testAccGroupMemberNames(t)
	resourceName := "aws_directoryservicedata_group_member.test"

	acctest.ParallelTest(ctx, t, resource.TestCase{
		PreCheck: func() {
			acctest.PreCheck(ctx, t)
			acctest.PreCheckDirectoryService(ctx, t)
		},
		ErrorCheck:               acctest.ErrorCheck(t, names.DirectoryServiceDataServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		CheckDestroy:             testAccCheckGroupMemberDestroy(ctx, t),
		Steps: []resource.TestStep{
			{
				Config: testAccGroupMemberConfig_basic(rName, domainName, groupName, userName),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckGroupMemberExists(ctx, t, resourceName),
					acctest.CheckFrameworkResourceDisappears(ctx, t, tfdirectoryservicedata.ResourceGroupMember, resourceName),
				),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

// Deleting the group out of band removes the membership with it, so the next
// plan must recreate the membership rather than fail.
func TestAccDirectoryServiceDataGroupMember_disappears_group(t *testing.T) {
	ctx := acctest.Context(t)
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	rName := acctest.RandomWithPrefix(t, acctest.ResourcePrefix)
	domainName := acctest.RandomDomainName(t)
	groupName, userName := testAccGroupMemberNames(t)
	resourceName := "aws_directoryservicedata_group_member.test"
	groupResourceName := "aws_directoryservicedata_group.test"

	acctest.ParallelTest(ctx, t, resource.TestCase{
		PreCheck: func() {
			acctest.PreCheck(ctx, t)
			acctest.PreCheckDirectoryService(ctx, t)
		},
		ErrorCheck:               acctest.ErrorCheck(t, names.DirectoryServiceDataServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		CheckDestroy:             testAccCheckGroupMemberDestroy(ctx, t),
		Steps: []resource.TestStep{
			{
				Config: testAccGroupMemberConfig_basic(rName, domainName, groupName, userName),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckGroupMemberExists(ctx, t, resourceName),
					acctest.CheckFrameworkResourceDisappears(ctx, t, tfdirectoryservicedata.ResourceGroup, groupResourceName),
				),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(groupResourceName, plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

func TestAccDirectoryServiceDataGroupMember_nestedGroup(t *testing.T) {
	ctx := acctest.Context(t)
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	rName := acctest.RandomWithPrefix(t, acctest.ResourcePrefix)
	domainName := acctest.RandomDomainName(t)
	parentName, childName := testAccGroupMemberNames(t)
	resourceName := "aws_directoryservicedata_group_member.test"

	acctest.ParallelTest(ctx, t, resource.TestCase{
		PreCheck: func() {
			acctest.PreCheck(ctx, t)
			acctest.PreCheckDirectoryService(ctx, t)
		},
		ErrorCheck:               acctest.ErrorCheck(t, names.DirectoryServiceDataServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		CheckDestroy:             testAccCheckGroupMemberDestroy(ctx, t),
		Steps: []resource.TestStep{
			{
				Config: testAccGroupMemberConfig_nestedGroup(rName, domainName, parentName, childName),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckGroupMemberExists(ctx, t, resourceName),
					resource.TestCheckResourceAttr(resourceName, names.AttrGroupName, parentName),
					resource.TestCheckResourceAttr(resourceName, "member_name", childName),
					resource.TestCheckResourceAttrPair(resourceName, "member_sid", "aws_directoryservicedata_group.child", "sid"),
					resource.TestCheckResourceAttr(resourceName, "member_type", string(awstypes.MemberTypeGroup)),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateIdFunc:                    acctest.AttrsImportStateIdFunc(resourceName, flex.ResourceIdSeparator, "directory_id", names.AttrGroupName, "member_name"),
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "directory_id",
				ImportStateVerifyIgnore:              []string{names.AttrTimeouts},
			},
		},
	})
}

// An explicit member_realm (here the directory's own domain, in a different
// letter case) and a member_name in a different letter case from the user's
// sAMAccountName must both match, and must not cause a diff after apply.
func TestAccDirectoryServiceDataGroupMember_caseInsensitive(t *testing.T) {
	ctx := acctest.Context(t)
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	rName := acctest.RandomWithPrefix(t, acctest.ResourcePrefix)
	domainName := acctest.RandomDomainName(t)
	groupName, userName := testAccGroupMemberNames(t)
	resourceName := "aws_directoryservicedata_group_member.test"
	memberName, memberRealm := strings.ToUpper(userName), strings.ToUpper(domainName)

	acctest.ParallelTest(ctx, t, resource.TestCase{
		PreCheck: func() {
			acctest.PreCheck(ctx, t)
			acctest.PreCheckDirectoryService(ctx, t)
		},
		ErrorCheck:               acctest.ErrorCheck(t, names.DirectoryServiceDataServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		CheckDestroy:             testAccCheckGroupMemberDestroy(ctx, t),
		Steps: []resource.TestStep{
			{
				Config: testAccGroupMemberConfig_memberRealm(rName, domainName, groupName, userName, memberName, memberRealm),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckGroupMemberExists(ctx, t, resourceName),
					resource.TestCheckResourceAttr(resourceName, "member_name", memberName),
					resource.TestCheckResourceAttr(resourceName, "member_realm", memberRealm),
					resource.TestCheckResourceAttrPair(resourceName, "member_sid", "aws_directoryservicedata_user.test", "sid"),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateIdFunc:                    acctest.AttrsImportStateIdFunc(resourceName, flex.ResourceIdSeparator, "directory_id", names.AttrGroupName, "member_name", "member_realm"),
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "directory_id",
				ImportStateVerifyIgnore:              []string{names.AttrTimeouts},
			},
		},
	})
}

// A second resource for a membership that already exists must fail with a
// pointer to import, rather than silently adopting it.
func TestAccDirectoryServiceDataGroupMember_alreadyMember(t *testing.T) {
	ctx := acctest.Context(t)
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	rName := acctest.RandomWithPrefix(t, acctest.ResourcePrefix)
	domainName := acctest.RandomDomainName(t)
	groupName, userName := testAccGroupMemberNames(t)

	acctest.ParallelTest(ctx, t, resource.TestCase{
		PreCheck: func() {
			acctest.PreCheck(ctx, t)
			acctest.PreCheckDirectoryService(ctx, t)
		},
		ErrorCheck:               acctest.ErrorCheck(t, names.DirectoryServiceDataServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		CheckDestroy:             testAccCheckGroupMemberDestroy(ctx, t),
		Steps: []resource.TestStep{
			{
				Config: testAccGroupMemberConfig_basic(rName, domainName, groupName, userName),
			},
			{
				Config:      testAccGroupMemberConfig_duplicate(rName, domainName, groupName, userName),
				ExpectError: regexache.MustCompile(`is already a member of`),
			},
		},
	})
}

func testAccGroupMemberNames(t *testing.T) (string, string) {
	t.Helper()

	// sAMAccountName is limited to 20 characters for users (and kept the same for
	// groups, so either can be used as a member).
	randomName := func() string {
		return fmt.Sprintf(
			"%s%s",
			acctest.ResourcePrefix,
			acctest.RandStringFromCharSet(t, 20-len(acctest.ResourcePrefix), "abcdefghijklmnopqrstuvwxyz0123456789"),
		)
	}

	return randomName(), randomName()
}

func testAccCheckGroupMemberDestroy(ctx context.Context, t *testing.T) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		conn := acctest.ProviderMeta(ctx, t).DirectoryServiceDataClient(ctx)

		for _, rs := range s.RootModule().Resources {
			if rs.Type != "aws_directoryservicedata_group_member" {
				continue
			}

			_, err := tfdirectoryservicedata.FindGroupMemberByFourPartKey(ctx, conn, rs.Primary.Attributes["directory_id"], rs.Primary.Attributes[names.AttrGroupName], rs.Primary.Attributes["member_name"], rs.Primary.Attributes["member_realm"])
			// The directory is usually destroyed in the same run, which surfaces as access denied.
			if retry.NotFound(err) || errs.IsA[*awstypes.AccessDeniedException](err) {
				continue
			}
			if err != nil {
				return smarterr.NewError(err)
			}

			return smarterr.NewError(errors.New("not destroyed"))
		}

		return nil
	}
}

func testAccCheckGroupMemberExists(ctx context.Context, t *testing.T, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return smarterr.NewError(errors.New("not found"))
		}

		conn := acctest.ProviderMeta(ctx, t).DirectoryServiceDataClient(ctx)
		_, err := tfdirectoryservicedata.FindGroupMemberByFourPartKey(ctx, conn, rs.Primary.Attributes["directory_id"], rs.Primary.Attributes[names.AttrGroupName], rs.Primary.Attributes["member_name"], rs.Primary.Attributes["member_realm"])

		if err != nil {
			return smarterr.NewError(err)
		}
		return nil
	}
}

func testAccGroupMemberConfig_base(rName, domainName string) string {
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
`, domainName),
	)
}

func testAccGroupMemberConfig_groupAndUser(rName, domainName, groupName, userName string) string {
	return acctest.ConfigCompose(
		testAccGroupMemberConfig_base(rName, domainName),
		fmt.Sprintf(`
resource "aws_directoryservicedata_group" "test" {
  directory_id     = aws_directory_service_directory.test.id
  sam_account_name = %[1]q
}

resource "aws_directoryservicedata_user" "test" {
  directory_id     = aws_directory_service_directory.test.id
  sam_account_name = %[2]q
}
`, groupName, userName),
	)
}

func testAccGroupMemberConfig_basic(rName, domainName, groupName, userName string) string {
	return acctest.ConfigCompose(
		testAccGroupMemberConfig_groupAndUser(rName, domainName, groupName, userName),
		`
resource "aws_directoryservicedata_group_member" "test" {
  directory_id = aws_directory_service_directory.test.id
  group_name   = aws_directoryservicedata_group.test.sam_account_name
  member_name  = aws_directoryservicedata_user.test.sam_account_name
}
`,
	)
}

func testAccGroupMemberConfig_duplicate(rName, domainName, groupName, userName string) string {
	return acctest.ConfigCompose(
		testAccGroupMemberConfig_basic(rName, domainName, groupName, userName),
		`
resource "aws_directoryservicedata_group_member" "duplicate" {
  directory_id = aws_directory_service_directory.test.id
  group_name   = aws_directoryservicedata_group.test.sam_account_name
  member_name  = aws_directoryservicedata_user.test.sam_account_name
}
`,
	)
}

func testAccGroupMemberConfig_memberRealm(rName, domainName, groupName, userName, memberName, memberRealm string) string {
	return acctest.ConfigCompose(
		testAccGroupMemberConfig_groupAndUser(rName, domainName, groupName, userName),
		fmt.Sprintf(`
resource "aws_directoryservicedata_group_member" "test" {
  directory_id = aws_directory_service_directory.test.id
  group_name   = aws_directoryservicedata_group.test.sam_account_name
  member_name  = %[1]q
  member_realm = %[2]q

  depends_on = [aws_directoryservicedata_user.test]
}
`, memberName, memberRealm),
	)
}

func testAccGroupMemberConfig_nestedGroup(rName, domainName, parentName, childName string) string {
	return acctest.ConfigCompose(
		testAccGroupMemberConfig_base(rName, domainName),
		fmt.Sprintf(`
resource "aws_directoryservicedata_group" "parent" {
  directory_id     = aws_directory_service_directory.test.id
  sam_account_name = %[1]q
}

resource "aws_directoryservicedata_group" "child" {
  directory_id     = aws_directory_service_directory.test.id
  sam_account_name = %[2]q
}

resource "aws_directoryservicedata_group_member" "test" {
  directory_id = aws_directory_service_directory.test.id
  group_name   = aws_directoryservicedata_group.parent.sam_account_name
  member_name  = aws_directoryservicedata_group.child.sam_account_name
}
`, parentName, childName),
	)
}
