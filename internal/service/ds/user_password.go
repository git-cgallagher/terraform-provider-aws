// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

package ds

import (
	"context"
	"errors"
	"time"

	"github.com/YakDriver/regexache"
	"github.com/YakDriver/smarterr"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/directoryservice"
	awstypes "github.com/aws/aws-sdk-go-v2/service/directoryservice/types"
	"github.com/aws/aws-sdk-go-v2/service/directoryservicedata"
	dsdatatypes "github.com/aws/aws-sdk-go-v2/service/directoryservicedata/types"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-provider-aws/internal/errs"
	"github.com/hashicorp/terraform-provider-aws/internal/errs/fwdiag"
	intflex "github.com/hashicorp/terraform-provider-aws/internal/flex"
	"github.com/hashicorp/terraform-provider-aws/internal/framework"
	fwflex "github.com/hashicorp/terraform-provider-aws/internal/framework/flex"
	"github.com/hashicorp/terraform-provider-aws/internal/retry"
	"github.com/hashicorp/terraform-provider-aws/internal/smerr"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
	inttypes "github.com/hashicorp/terraform-provider-aws/internal/types"
	"github.com/hashicorp/terraform-provider-aws/names"
)

// @FrameworkResource("aws_directory_service_user_password", name="User Password")
// @IdentityAttribute("directory_id")
// @IdentityAttribute("user_name")
// @ImportIDHandler("userPasswordImportID")
// @Testing(generator=false)
// @Testing(preCheck="acctest.PreCheckDirectoryService")
// @Testing(domainTfVar="domain")
// @Testing(hasNoPreExistingResource=true)
// @Testing(checkDestroyNoop=true)
// @Testing(importIgnore="password_wo_version")
// @Testing(importStateIdAttributes="directory_id;user_name", importStateIdAttributesSep="flex.ResourceIdSeparator")
func newUserPasswordResource(_ context.Context) (resource.ResourceWithConfigure, error) {
	r := &userPasswordResource{}

	r.SetDefaultCreateTimeout(5 * time.Minute)
	r.SetDefaultUpdateTimeout(5 * time.Minute)

	return r, nil
}

const (
	userPasswordResourceIDPartCount = 2
	userPasswordPropagationTimeout  = 2 * time.Minute
)

type userPasswordResource struct {
	framework.ResourceWithModel[userPasswordResourceModel]
	framework.WithImportByIdentity
	// A password can't be removed, and removing this resource must not disable
	// the user, so delete only forgets the resource.
	framework.WithNoOpDelete
	framework.WithTimeouts
}

func (r *userPasswordResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"directory_id": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					directoryIDValidator,
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"password_wo": schema.StringAttribute{
				Required:  true,
				Sensitive: true,
				WriteOnly: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 127),
				},
			},
			// The only way to tell Terraform that the write-only password changed.
			"password_wo_version": schema.Int64Attribute{
				Required: true,
			},
			names.AttrUserName: schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 64),
					stringvalidator.RegexMatches(regexache.MustCompile(`^[^\\"/\[\]:;|=,+*?<>@]+$`), "must not contain any of the following characters: \\ \" / [ ] : ; | = , + * ? < > @"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_sid": schema.StringAttribute{
				Computed: true,
			},
		},
		Blocks: map[string]schema.Block{
			names.AttrTimeouts: timeouts.Block(ctx, timeouts.Opts{
				Create: true,
				Update: true,
			}),
		},
	}
}

func (r *userPasswordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config userPasswordResourceModel
	smerr.AddEnrich(ctx, &resp.Diagnostics, req.Plan.Get(ctx, &plan))
	smerr.AddEnrich(ctx, &resp.Diagnostics, req.Config.Get(ctx, &config))
	if resp.Diagnostics.HasError() {
		return
	}

	directoryID, userName := fwflex.StringValueFromFramework(ctx, plan.DirectoryID), fwflex.StringValueFromFramework(ctx, plan.UserName)
	id := userPasswordResourceID(directoryID, userName)

	// The write-only value is only in Config, never in Plan or State.
	err := resetUserPassword(ctx, r.Meta().DSClient(ctx), directoryID, userName, fwflex.StringValueFromFramework(ctx, config.PasswordWO), r.CreateTimeout(ctx, plan.Timeouts))
	if err != nil {
		smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, id)
		return
	}

	sid, d := r.userSID(ctx, directoryID, userName)
	smerr.AddEnrich(ctx, &resp.Diagnostics, d)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.UserSID = sid

	smerr.AddEnrich(ctx, &resp.Diagnostics, resp.State.Set(ctx, plan))
}

func (r *userPasswordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userPasswordResourceModel
	smerr.AddEnrich(ctx, &resp.Diagnostics, req.State.Get(ctx, &state))
	if resp.Diagnostics.HasError() {
		return
	}

	directoryID, userName := fwflex.StringValueFromFramework(ctx, state.DirectoryID), fwflex.StringValueFromFramework(ctx, state.UserName)
	id := userPasswordResourceID(directoryID, userName)

	sid, err := findUserSIDByTwoPartKey(ctx, r.Meta().DirectoryServiceDataClient(ctx), directoryID, userName)

	if isDirectoryDataAccessDisabled(err) {
		// Without Directory Service Data the user can't be looked up, so keep the
		// state as it is rather than fail every plan.
		tflog.Debug(ctx, "Directory Service Data access is disabled; not checking that the user still exists", map[string]any{
			"directory_id":     directoryID,
			names.AttrUserName: userName,
		})
		return
	}
	if retry.NotFound(err) {
		smerr.AddOne(ctx, &resp.Diagnostics, fwdiag.NewResourceNotFoundWarningDiagnostic(err), smerr.ID, id)
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, id)
		return
	}

	// A user deleted and created again under the same name gets a new SID and
	// starts disabled, so the password must be set again.
	if userReplaced(state.UserSID.ValueString(), sid) {
		smerr.AddOne(ctx, &resp.Diagnostics, fwdiag.NewResourceNotFoundWarningDiagnostic(smarterr.Errorf("user %q was replaced (SID %s, was %s)", userName, sid, state.UserSID.ValueString())), smerr.ID, id)
		resp.State.RemoveResource(ctx)
		return
	}

	state.UserSID = types.StringValue(sid)

	smerr.AddEnrich(ctx, &resp.Diagnostics, resp.State.Set(ctx, &state))
}

func (r *userPasswordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config userPasswordResourceModel
	smerr.AddEnrich(ctx, &resp.Diagnostics, req.Plan.Get(ctx, &plan))
	smerr.AddEnrich(ctx, &resp.Diagnostics, req.State.Get(ctx, &state))
	smerr.AddEnrich(ctx, &resp.Diagnostics, req.Config.Get(ctx, &config))
	if resp.Diagnostics.HasError() {
		return
	}

	directoryID, userName := fwflex.StringValueFromFramework(ctx, plan.DirectoryID), fwflex.StringValueFromFramework(ctx, plan.UserName)

	if !plan.PasswordWOVersion.Equal(state.PasswordWOVersion) {
		err := resetUserPassword(ctx, r.Meta().DSClient(ctx), directoryID, userName, fwflex.StringValueFromFramework(ctx, config.PasswordWO), r.UpdateTimeout(ctx, plan.Timeouts))
		if err != nil {
			smerr.AddError(ctx, &resp.Diagnostics, err, smerr.ID, userPasswordResourceID(directoryID, userName))
			return
		}
	}

	plan.UserSID = state.UserSID

	smerr.AddEnrich(ctx, &resp.Diagnostics, resp.State.Set(ctx, &plan))
}

// userSID returns the user's SID, or null when Directory Service Data access is
// disabled. After a password reset the user may not have replicated to the
// domain controller that serves the lookup yet, so retry not found briefly.
func (r *userPasswordResource) userSID(ctx context.Context, directoryID, userName string) (types.String, diag.Diagnostics) {
	var diags diag.Diagnostics
	conn := r.Meta().DirectoryServiceDataClient(ctx)

	sid, err := tfresource.RetryWhenNotFound(ctx, userPasswordPropagationTimeout, func(ctx context.Context) (string, error) {
		return findUserSIDByTwoPartKey(ctx, conn, directoryID, userName)
	})

	if isDirectoryDataAccessDisabled(err) {
		return types.StringNull(), diags
	}
	if err != nil {
		smerr.AddError(ctx, &diags, err, smerr.ID, userPasswordResourceID(directoryID, userName))
		return types.StringNull(), diags
	}

	return types.StringValue(sid), diags
}

// resetUserPassword sets the user's password, which also enables a disabled
// user. A user created moments earlier may not have replicated to the domain
// controller that serves the request yet, so retry UserDoesNotExistException.
// Errors from the API never include the password.
func resetUserPassword(ctx context.Context, conn *directoryservice.Client, directoryID, userName, password string, timeout time.Duration) error {
	input := directoryservice.ResetUserPasswordInput{
		DirectoryId: aws.String(directoryID),
		NewPassword: aws.String(password),
		UserName:    aws.String(userName),
	}

	_, err := tfresource.RetryWhenIsA[*directoryservice.ResetUserPasswordOutput, *awstypes.UserDoesNotExistException](ctx, timeout, func(ctx context.Context) (*directoryservice.ResetUserPasswordOutput, error) {
		return conn.ResetUserPassword(ctx, &input)
	})

	return smarterr.NewError(err)
}

// findUserSIDByTwoPartKey looks the user up through Directory Service Data,
// the only API that describes directory users.
func findUserSIDByTwoPartKey(ctx context.Context, conn *directoryservicedata.Client, directoryID, userName string) (string, error) {
	input := directoryservicedata.DescribeUserInput{
		DirectoryId:    aws.String(directoryID),
		SAMAccountName: aws.String(userName),
	}

	output, err := conn.DescribeUser(ctx, &input)

	if errs.IsA[*dsdatatypes.ResourceNotFoundException](err) {
		return "", smarterr.NewError(&retry.NotFoundError{LastError: err})
	}
	if err != nil {
		return "", smarterr.NewError(err)
	}
	if output == nil || output.SID == nil {
		return "", smarterr.NewError(tfresource.NewEmptyResultError())
	}

	return aws.ToString(output.SID), nil
}

func isDirectoryDataAccessDisabled(err error) bool {
	v, ok := errors.AsType[*dsdatatypes.AccessDeniedException](err)

	return ok && v.Reason == dsdatatypes.AccessDeniedReasonDataDisabled
}

// userReplaced reports whether the user now has a different SID from the one
// recorded when the password was set. An unknown SID on either side (import,
// or Directory Service Data access disabled at create) is not a replacement.
func userReplaced(stateSID, currentSID string) bool {
	return stateSID != "" && currentSID != "" && stateSID != currentSID
}

type userPasswordResourceModel struct {
	framework.WithRegionModel
	DirectoryID       types.String   `tfsdk:"directory_id"`
	PasswordWO        types.String   `tfsdk:"password_wo"`
	PasswordWOVersion types.Int64    `tfsdk:"password_wo_version"`
	Timeouts          timeouts.Value `tfsdk:"timeouts"`
	UserName          types.String   `tfsdk:"user_name"`
	UserSID           types.String   `tfsdk:"user_sid"`
}

var (
	_ inttypes.ImportIDParser = userPasswordImportID{}
)

type userPasswordImportID struct{}

func (userPasswordImportID) Parse(id string) (string, map[string]any, error) {
	parts, err := intflex.ExpandResourceId(id, userPasswordResourceIDPartCount, false)
	if err != nil {
		return "", nil, smarterr.Errorf("unexpected import ID %q: expected format <directory_id>,<user_name>: %w", id, err)
	}

	result := map[string]any{
		"directory_id":     parts[0],
		names.AttrUserName: parts[1],
	}

	return id, result, nil
}

func userPasswordResourceID(directoryID, userName string) string {
	id, _ := intflex.FlattenResourceId(
		[]string{directoryID, userName},
		userPasswordResourceIDPartCount,
		false,
	)

	return id
}
