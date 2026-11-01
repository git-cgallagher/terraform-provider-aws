// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

package directoryservicedata

import (
	"context"
	"strings"
	"time"

	"github.com/YakDriver/regexache"
	"github.com/YakDriver/smarterr"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/directoryservicedata"
	awstypes "github.com/aws/aws-sdk-go-v2/service/directoryservicedata/types"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-provider-aws/internal/create"
	"github.com/hashicorp/terraform-provider-aws/internal/errs"
	"github.com/hashicorp/terraform-provider-aws/internal/errs/fwdiag"
	intflex "github.com/hashicorp/terraform-provider-aws/internal/flex"
	"github.com/hashicorp/terraform-provider-aws/internal/framework"
	fwflex "github.com/hashicorp/terraform-provider-aws/internal/framework/flex"
	fwtypes "github.com/hashicorp/terraform-provider-aws/internal/framework/types"
	"github.com/hashicorp/terraform-provider-aws/internal/retry"
	"github.com/hashicorp/terraform-provider-aws/internal/smerr"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
	inttypes "github.com/hashicorp/terraform-provider-aws/internal/types"
	"github.com/hashicorp/terraform-provider-aws/names"
)

// @FrameworkResource("aws_directoryservicedata_group_member", name="Group Member")
// @IdentityAttribute("directory_id")
// @IdentityAttribute("group_name")
// @IdentityAttribute("member_name")
// @IdentityAttribute("member_realm", optional="true")
// @ImportIDHandler("groupMemberImportID")
// @Testing(hasNoPreExistingResource=true)
// @Testing(generator=false)
// @Testing(domainTfVar="directoryDomain")
// @Testing(importStateIdAttributes="directory_id;group_name;member_name", importStateIdAttributesSep="flex.ResourceIdSeparator")
// @Testing(preCheck="github.com/hashicorp/terraform-provider-aws/internal/acctest;acctest.PreCheckDirectoryService")
func newGroupMemberResource(_ context.Context) (resource.ResourceWithConfigure, error) {
	r := &groupMemberResource{}

	r.SetDefaultCreateTimeout(5 * time.Minute)

	return r, nil
}

const (
	groupMemberResourceIDPartCount          = 3
	groupMemberResourceIDPartCountWithRealm = 4
	groupMemberPropagationTimeout           = 2 * time.Minute
	groupMemberImportIDFormat               = "<directory_id>,<group_name>,<member_name>[,<member_realm>]"
)

type groupMemberResource struct {
	framework.ResourceWithModel[groupMemberResourceModel]
	framework.WithImportByIdentity
	// There is no API to update a group membership, so every argument forces replacement.
	framework.WithNoUpdate
	framework.WithTimeouts
}

func (r *groupMemberResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"directory_id": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexache.MustCompile(`^d-[0-9a-f]{10}$`), "must be a valid Directory Service directory ID"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			names.AttrGroupName: schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 64),
					stringvalidator.RegexMatches(regexache.MustCompile(`^[^:;|=+"*?<>/\\,\[\]@]+$`), "must not contain any of the following characters: : ; | = + \" * ? < > / \\ , [ ] @"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"member_name": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 63),
					stringvalidator.RegexMatches(regexache.MustCompile(`^[^:;|=+"*?<>/\\,\[\]@]+$`), "must not contain any of the following characters: : ; | = + \" * ? < > / \\ , [ ] @"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			// The API defaults the realm to the directory's own domain. Leaving it
			// null (rather than Computed) keeps that default without a diff, and the
			// configured value is never overwritten from the API, so the API's
			// case-insensitive handling of realms can't cause a diff either.
			"member_realm": schema.StringAttribute{
				Optional: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 255),
					stringvalidator.RegexMatches(regexache.MustCompile(`^([0-9A-Za-z]+[.-])+[0-9A-Za-z]+\.?$`), "must be a valid domain name"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"member_sid": schema.StringAttribute{
				Computed: true,
			},
			"member_type": schema.StringAttribute{
				Computed:   true,
				CustomType: fwtypes.StringEnumType[awstypes.MemberType](),
			},
		},
		Blocks: map[string]schema.Block{
			names.AttrTimeouts: timeouts.Block(ctx, timeouts.Opts{
				Create: true,
			}),
		},
	}
}

func (r *groupMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	conn := r.Meta().DirectoryServiceDataClient(ctx)

	var plan groupMemberResourceModel
	smerr.AddEnrich(ctx, &resp.Diagnostics, req.Plan.Get(ctx, &plan))
	if resp.Diagnostics.HasError() {
		return
	}

	directoryID, groupName, memberName, memberRealm := plan.parts()
	id := groupMemberResourceID(directoryID, groupName, memberName, memberRealm)

	input := directoryservicedata.AddGroupMemberInput{
		ClientToken: aws.String(create.UniqueId(ctx)),
		DirectoryId: aws.String(directoryID),
		GroupName:   aws.String(groupName),
		MemberName:  aws.String(memberName),
		MemberRealm: fwflex.StringFromFramework(ctx, plan.MemberRealm),
	}

	// A group or user created moments earlier may not have replicated to the
	// domain controller that serves this request yet. Retries reuse the same
	// client token, which the API treats as the same request.
	timeout := r.CreateTimeout(ctx, plan.Timeouts)
	_, err := tfresource.RetryWhenIsA[*directoryservicedata.AddGroupMemberOutput, *awstypes.ResourceNotFoundException](ctx, timeout, func(ctx context.Context) (*directoryservicedata.AddGroupMemberOutput, error) {
		return conn.AddGroupMember(ctx, &input)
	})

	if errs.IsA[*awstypes.ConflictException](err) {
		smerr.AddError(ctx, &resp.Diagnostics, smarterr.Errorf("%s is already a member of %s; import it with the ID %q to manage it: %w", memberName, groupName, id, err), smerr.ID, id)
		return
	}
	if err != nil {
		smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, id)
		return
	}

	member, err := tfresource.RetryWhenNotFound(ctx, groupMemberPropagationTimeout, func(ctx context.Context) (*awstypes.Member, error) {
		return findGroupMemberByFourPartKey(ctx, conn, directoryID, groupName, memberName, memberRealm)
	})
	if err != nil {
		smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, id)
		return
	}

	plan.flatten(member)

	smerr.AddEnrich(ctx, &resp.Diagnostics, resp.State.Set(ctx, plan))
}

func (r *groupMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	conn := r.Meta().DirectoryServiceDataClient(ctx)

	var state groupMemberResourceModel
	smerr.AddEnrich(ctx, &resp.Diagnostics, req.State.Get(ctx, &state))
	if resp.Diagnostics.HasError() {
		return
	}

	directoryID, groupName, memberName, memberRealm := state.parts()
	id := groupMemberResourceID(directoryID, groupName, memberName, memberRealm)

	member, err := findGroupMemberByFourPartKey(ctx, conn, directoryID, groupName, memberName, memberRealm)

	if retry.NotFound(err) {
		smerr.AddOne(ctx, &resp.Diagnostics, fwdiag.NewResourceNotFoundWarningDiagnostic(err), smerr.ID, id)
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, id)
		return
	}

	state.flatten(member)

	smerr.AddEnrich(ctx, &resp.Diagnostics, resp.State.Set(ctx, &state))
}

func (r *groupMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	conn := r.Meta().DirectoryServiceDataClient(ctx)

	var state groupMemberResourceModel
	smerr.AddEnrich(ctx, &resp.Diagnostics, req.State.Get(ctx, &state))
	if resp.Diagnostics.HasError() {
		return
	}

	directoryID, groupName, memberName, memberRealm := state.parts()

	input := directoryservicedata.RemoveGroupMemberInput{
		ClientToken: aws.String(create.UniqueId(ctx)),
		DirectoryId: aws.String(directoryID),
		GroupName:   aws.String(groupName),
		MemberName:  aws.String(memberName),
		MemberRealm: fwflex.StringFromFramework(ctx, state.MemberRealm),
	}

	_, err := conn.RemoveGroupMember(ctx, &input)
	if errs.IsA[*awstypes.ResourceNotFoundException](err) {
		return
	}
	if err != nil {
		smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, groupMemberResourceID(directoryID, groupName, memberName, memberRealm))
		return
	}
}

// findGroupMemberByFourPartKey returns the direct member of a group. It lists the
// group's members rather than the member's groups because only ListGroupMembers
// documents MemberRealm as a filter for members from a trusted domain.
// sAMAccountName matching is case-insensitive, as it is in Active Directory.
func findGroupMemberByFourPartKey(ctx context.Context, conn *directoryservicedata.Client, directoryID, groupName, memberName, memberRealm string) (*awstypes.Member, error) {
	input := directoryservicedata.ListGroupMembersInput{
		DirectoryId:    aws.String(directoryID),
		SAMAccountName: aws.String(groupName),
	}
	if memberRealm != "" {
		input.MemberRealm = aws.String(memberRealm)
	}

	pages := directoryservicedata.NewListGroupMembersPaginator(conn, &input)
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)

		if errs.IsA[*awstypes.ResourceNotFoundException](err) {
			return nil, smarterr.NewError(&retry.NotFoundError{LastError: err})
		}
		if err != nil {
			return nil, smarterr.NewError(err)
		}

		if member := findMemberBySAMAccountName(page.Members, memberName); member != nil {
			return member, nil
		}
	}

	return nil, smarterr.NewError(&retry.NotFoundError{})
}

func findMemberBySAMAccountName(members []awstypes.Member, samAccountName string) *awstypes.Member {
	for _, v := range members {
		if strings.EqualFold(aws.ToString(v.SAMAccountName), samAccountName) {
			return &v
		}
	}

	return nil
}

type groupMemberResourceModel struct {
	framework.WithRegionModel
	DirectoryID types.String                            `tfsdk:"directory_id"`
	GroupName   types.String                            `tfsdk:"group_name"`
	MemberName  types.String                            `tfsdk:"member_name"`
	MemberRealm types.String                            `tfsdk:"member_realm"`
	MemberSID   types.String                            `tfsdk:"member_sid"`
	MemberType  fwtypes.StringEnum[awstypes.MemberType] `tfsdk:"member_type"`
	Timeouts    timeouts.Value                          `tfsdk:"timeouts"`
}

func (m groupMemberResourceModel) parts() (directoryID, groupName, memberName, memberRealm string) {
	return m.DirectoryID.ValueString(), m.GroupName.ValueString(), m.MemberName.ValueString(), m.MemberRealm.ValueString()
}

// flatten sets only the computed attributes. The configured names are kept as
// written so that a difference in letter case doesn't force replacement.
func (m *groupMemberResourceModel) flatten(member *awstypes.Member) {
	m.MemberSID = types.StringPointerValue(member.SID)
	m.MemberType = fwtypes.StringEnumValue(member.MemberType)
}

var (
	_ inttypes.ImportIDParser = groupMemberImportID{}
)

type groupMemberImportID struct{}

func (groupMemberImportID) Parse(id string) (string, map[string]any, error) {
	parts := strings.Split(id, intflex.ResourceIdSeparator)
	if len(parts) != groupMemberResourceIDPartCount && len(parts) != groupMemberResourceIDPartCountWithRealm {
		return "", nil, smarterr.Errorf("unexpected import ID %q: expected format %s", id, groupMemberImportIDFormat)
	}
	for _, part := range parts {
		if part == "" {
			return "", nil, smarterr.Errorf("unexpected import ID %q: expected format %s with no empty parts", id, groupMemberImportIDFormat)
		}
	}

	result := map[string]any{
		"directory_id":      parts[0],
		names.AttrGroupName: parts[1],
		"member_name":       parts[2],
	}
	if len(parts) == groupMemberResourceIDPartCountWithRealm {
		result["member_realm"] = parts[3]
	}

	return id, result, nil
}

func groupMemberResourceID(directoryID, groupName, memberName, memberRealm string) string {
	parts := []string{directoryID, groupName, memberName}
	if memberRealm != "" {
		parts = append(parts, memberRealm)
	}

	return strings.Join(parts, intflex.ResourceIdSeparator)
}
