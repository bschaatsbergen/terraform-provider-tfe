package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/go-tfe"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &resourceTFEDataRetentionPolicy{}
var _ resource.ResourceWithConfigure = &resourceTFEDataRetentionPolicy{}
var _ resource.ResourceWithImportState = &resourceTFEDataRetentionPolicy{}
var _ resource.ResourceWithModifyPlan = &resourceTFEDataRetentionPolicy{}

func NewDataRetentionPolicyResource() resource.Resource {
	return &resourceTFEDataRetentionPolicy{}
}

// resourceTFEDataRetentionPolicy implements the tfe_data_retention_policy resource type
type resourceTFEDataRetentionPolicy struct {
	config ConfiguredClient
}

func (r *resourceTFEDataRetentionPolicy) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_data_retention_policy"
}

func (r *resourceTFEDataRetentionPolicy) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	modifyPlanForDefaultOrganizationChange(ctx, r.config.Organization, req.State, req.Config, req.Plan, resp)
}

func (r *resourceTFEDataRetentionPolicy) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the data retention policies for a specific workspace or an the entire organization.",
		Version:     1,

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "ID of the Data Retention Policy.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"organization": schema.StringAttribute{
				Description: "Name of the organization. If omitted, organization must be defined in the provider config.",
				Optional:    true,
			},
			"workspace_id": schema.StringAttribute{
				Description: "ID of the workspace that the data retention policy should apply to. If omitted, the data retention policy will apply to the entire organization.",
				Optional:    true,
			},
		},
		Blocks: map[string]schema.Block{
			"delete_older_than": schema.SingleNestedBlock{
				Description: "Sets the maximum number of days, months, years data is allowed to exist before it is scheduled for deletion. Cannot be configured if the dont_delete attribute is also configured.",
				Attributes: map[string]schema.Attribute{
					"days": schema.NumberAttribute{
						Description: "Number of days",
						Required:    true,
					},
				},
				Validators: []validator.Object{
					objectvalidator.ExactlyOneOf(
						path.MatchRelative().AtParent().AtName("dont_delete"),
					),
				},
			},
			"dont_delete": schema.SingleNestedBlock{
				Attributes: map[string]schema.Attribute{},
				Validators: []validator.Object{
					objectvalidator.ExactlyOneOf(
						path.MatchRelative().AtParent().AtName("delete_older_than"),
					),
				},
			},
		},
	}
}

// Configure implements resource.ResourceWithConfigure
func (r *resourceTFEDataRetentionPolicy) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(ConfiguredClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected resource Configure type",
			fmt.Sprintf("Expected tfe.ConfiguredClient, got %T. This is a bug in the tfe provider, so please report it on GitHub.", req.ProviderData),
		)
	}
	r.config = client
}

func (r *resourceTFEDataRetentionPolicy) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan modelTFEDataRetentionPolicy

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	var organization string
	if plan.WorkspaceId.IsNull() {
		resp.Diagnostics.Append(r.config.dataOrDefaultOrganization(ctx, req.Plan, &organization)...)
		plan.Organization = types.StringValue(organization)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.DeleteOlderThan.IsNull() {
		r.createDeleteOlderThanRetentionPolicy(ctx, plan, resp)
		return
	}

	if !plan.DontDelete.IsNull() {
		r.createDontDeleteRetentionPolicy(ctx, plan, resp)
		return
	}

}

func (r *resourceTFEDataRetentionPolicy) createDeleteOlderThanRetentionPolicy(ctx context.Context, plan modelTFEDataRetentionPolicy, resp *resource.CreateResponse) {
	deleteOlderThan := &modelTFEDeleteOlderThan{}

	diags := plan.DeleteOlderThan.As(ctx, &deleteOlderThan, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	deleteOlderThanDays, _ := deleteOlderThan.Days.ValueBigFloat().Int64()
	options := tfe.DataRetentionPolicyDeleteOlderSetOptions{
		DeleteOlderThanNDays: int(deleteOlderThanDays),
	}

	tflog.Debug(ctx, "Creating data retention policy")
	var dataRetentionPolicy *tfe.DataRetentionPolicyDeleteOlder
	var err error
	if plan.WorkspaceId.IsNull() {
		dataRetentionPolicy, err = r.config.Client.Organizations.SetDataRetentionPolicyDeleteOlder(ctx, plan.Organization.ValueString(), options)
	} else {
		dataRetentionPolicy, err = r.config.Client.Workspaces.SetDataRetentionPolicyDeleteOlder(ctx, plan.WorkspaceId.ValueString(), options)
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to create data retention policy", err.Error())
		return
	}

	result, diags := modelFromTFEDataRetentionPolicyDeleteOlder(ctx, plan, dataRetentionPolicy)
	if diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &result)...)
}

func (r *resourceTFEDataRetentionPolicy) createDontDeleteRetentionPolicy(ctx context.Context, plan modelTFEDataRetentionPolicy, resp *resource.CreateResponse) {
	deleteOlderThan := &modelTFEDeleteOlderThan{}

	diags := plan.DeleteOlderThan.As(ctx, &deleteOlderThan, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	options := tfe.DataRetentionPolicyDontDeleteSetOptions{}

	tflog.Debug(ctx, "Creating data retention policy")
	var dataRetentionPolicy *tfe.DataRetentionPolicyDontDelete
	var err error
	if plan.WorkspaceId.IsNull() {
		dataRetentionPolicy, err = r.config.Client.Organizations.SetDataRetentionPolicyDontDelete(ctx, plan.Organization.ValueString(), options)
	} else {
		dataRetentionPolicy, err = r.config.Client.Workspaces.SetDataRetentionPolicyDontDelete(ctx, plan.WorkspaceId.ValueString(), options)
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to create data retention policy", err.Error())
		return
	}

	result := modelFromTFEDataRetentionPolicyDontDelete(plan, dataRetentionPolicy)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &result)...)
}

func (r *resourceTFEDataRetentionPolicy) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state modelTFEDataRetentionPolicy

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	var policy *tfe.DataRetentionPolicyChoice
	var err error
	if state.WorkspaceId.IsNull() {
		policy, err = r.config.Client.Organizations.ReadDataRetentionPolicyChoice(ctx, state.Organization.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Failed to read data retention policy", err.Error())
			return
		}
	} else {
		policy, err = r.config.Client.Workspaces.ReadDataRetentionPolicyChoice(ctx, state.WorkspaceId.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Failed to read data retention policy", err.Error())
			return
		}
	}
	result, diags := modelFromTFEDataRetentionPolicyChoice(ctx, state, policy)
	if diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &result)...)
}

func (r *resourceTFEDataRetentionPolicy) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// If the resource does not support modification and should always be recreated on
	// configuration value updates, the Update logic can be left empty and ensure all
	// configurable schema attributes implement the resource.RequiresReplace()
	// attribute plan modifier.
	resp.Diagnostics.AddError("Update not supported", "The update operation is not supported on this resource. This is a bug in the provider.")
}

func (r *resourceTFEDataRetentionPolicy) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	//var state modelTFERegistryGPGKey
	//
	//// Read Terraform prior state data into the model
	//resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	//
	//if resp.Diagnostics.HasError() {
	//	return
	//}
	//
	//keyID := tfe.GPGKeyID{
	//	RegistryName: "private",
	//	Namespace:    state.Organization.ValueString(),
	//	KeyID:        state.ID.ValueString(),
	//}
	//
	//tflog.Debug(ctx, "Deleting private registry GPG key")
	//err := r.config.Client.GPGKeys.Delete(ctx, keyID)
	//if err != nil {
	//	resp.Diagnostics.AddError("Unable to delete private registry GPG key", err.Error())
	//	return
	//}
}

func (r *resourceTFEDataRetentionPolicy) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	s := strings.SplitN(req.ID, "/", 2)
	if len(s) != 2 {
		resp.Diagnostics.AddError(
			"Error importing variable",
			fmt.Sprintf("Invalid variable import format: %s (expected <ORGANIZATION>/<KEY ID>)", req.ID),
		)
		return
	}
	org := s[0]
	id := s[1]

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization"), org)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
