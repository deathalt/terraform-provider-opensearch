package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/structure"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/olivere/elastic/uritemplates"
)

func resourceOpensearchDashboardWorkspace() *schema.Resource {
	return &schema.Resource{
		Description: "Provides an OpenSearch Dashboards workspace resource.",
		Create:      resourceOpensearchDashboardWorkspaceCreate,
		Read:        resourceOpensearchDashboardWorkspaceRead,
		Update:      resourceOpensearchDashboardWorkspaceUpdate,
		Delete:      resourceOpensearchDashboardWorkspaceDelete,
		Schema: map[string]*schema.Schema{
			"name": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Workspace name.",
			},
			"description": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Workspace description.",
			},
			"features": {
				Type:        schema.TypeSet,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Workspace features, such as use-case-all or use-case-observability.",
			},
			"tenant_name": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Default:     "global",
				Description: "OpenSearch Dashboards tenant name where the workspace is stored. Defaults to the global tenant.",
			},
			"permissions": {
				Type:             schema.TypeString,
				Optional:         true,
				ValidateFunc:     validation.StringIsJSON,
				DiffSuppressFunc: suppressEquivalentJSON,
				StateFunc: func(v interface{}) string {
					json, _ := structure.NormalizeJsonString(v)
					return json
				},
				Description: "Workspace permissions object as JSON.",
			},
		},
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
	}
}

func resourceOpensearchDashboardWorkspaceCreate(d *schema.ResourceData, meta interface{}) error {
	request, err := dashboardWorkspaceRequestBody(d)
	if err != nil {
		return err
	}

	var response dashboardWorkspaceCreateResponse
	if err := dashboardsRequestWithHeaders(meta.(*ProviderConf), http.MethodPost, "/api/workspaces", request, &response, dashboardWorkspaceHeaders(d)); err != nil {
		return err
	}
	if !response.Success || response.Result.ID == "" {
		if response.Error != "" {
			return fmt.Errorf("workspace create failed: %s", response.Error)
		}
		return fmt.Errorf("unexpected workspace create response: %+v", response)
	}

	d.SetId(response.Result.ID)
	return resourceOpensearchDashboardWorkspaceRead(d, meta)
}

func resourceOpensearchDashboardWorkspaceRead(d *schema.ResourceData, meta interface{}) error {
	workspace, err := getDashboardWorkspace(d.Id(), meta, dashboardWorkspaceHeaders(d))
	if err != nil {
		if errors.Is(err, errDashboardsNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}

	ds := &resourceDataSetter{d: d}
	ds.set("name", workspace.Name)
	ds.set("description", workspace.Description)
	ds.set("features", flattenStringSet(workspace.Features))
	ds.set("tenant_name", d.Get("tenant_name").(string))
	return ds.err
}

func resourceOpensearchDashboardWorkspaceUpdate(d *schema.ResourceData, meta interface{}) error {
	request, err := dashboardWorkspaceRequestBody(d)
	if err != nil {
		return err
	}

	path, err := uritemplates.Expand("/api/workspaces/{id}", map[string]string{
		"id": d.Id(),
	})
	if err != nil {
		return fmt.Errorf("error building URL path for workspace: %+v", err)
	}

	var response dashboardWorkspaceBoolResponse
	if err := dashboardsRequestWithHeaders(meta.(*ProviderConf), http.MethodPut, path, request, &response, dashboardWorkspaceHeaders(d)); err != nil {
		return err
	}
	if !response.Success || !response.Result {
		if response.Error != "" {
			return fmt.Errorf("workspace update failed: %s", response.Error)
		}
		return fmt.Errorf("unexpected workspace update response: %+v", response)
	}

	return resourceOpensearchDashboardWorkspaceRead(d, meta)
}

func resourceOpensearchDashboardWorkspaceDelete(d *schema.ResourceData, meta interface{}) error {
	path, err := uritemplates.Expand("/api/workspaces/{id}", map[string]string{
		"id": d.Id(),
	})
	if err != nil {
		return fmt.Errorf("error building URL path for workspace: %+v", err)
	}

	var response dashboardWorkspaceBoolResponse
	if err := dashboardsRequestWithHeaders(meta.(*ProviderConf), http.MethodDelete, path, nil, &response, dashboardWorkspaceHeaders(d)); err != nil {
		if errors.Is(err, errDashboardsNotFound) {
			return nil
		}
		return err
	}
	if !response.Success || !response.Result {
		if response.Error != "" {
			return fmt.Errorf("workspace delete failed: %s", response.Error)
		}
		return fmt.Errorf("unexpected workspace delete response: %+v", response)
	}
	return nil
}

func getDashboardWorkspace(id string, meta interface{}, headers map[string]string) (*dashboardWorkspace, error) {
	path, err := uritemplates.Expand("/api/workspaces/{id}", map[string]string{
		"id": id,
	})
	if err != nil {
		return nil, fmt.Errorf("error building URL path for workspace: %+v", err)
	}

	var response dashboardWorkspaceGetResponse
	if err := dashboardsRequestWithHeaders(meta.(*ProviderConf), http.MethodGet, path, nil, &response, headers); err != nil {
		return nil, err
	}
	if !response.Success {
		if response.Error == "workspace not found" {
			return nil, errDashboardsNotFound
		}
		if response.Error != "" {
			return nil, fmt.Errorf("workspace get failed: %s", response.Error)
		}
		return nil, fmt.Errorf("unexpected workspace get response: %+v", response)
	}
	return &response.Result, nil
}

func dashboardWorkspaceHeaders(d *schema.ResourceData) map[string]string {
	return dashboardTenantHeaders(d.Get("tenant_name").(string))
}

func dashboardWorkspaceRequestBody(d *schema.ResourceData) (*dashboardWorkspaceRequest, error) {
	request := &dashboardWorkspaceRequest{
		Attributes: dashboardWorkspaceAttributes{
			Name:        d.Get("name").(string),
			Description: d.Get("description").(string),
			Features:    expandStringList(d.Get("features").(*schema.Set).List()),
		},
	}

	if permissions, ok := d.GetOk("permissions"); ok {
		var permissionsBody map[string]interface{}
		if err := json.Unmarshal([]byte(permissions.(string)), &permissionsBody); err != nil {
			return nil, fmt.Errorf("error unmarshalling permissions: %+v", err)
		}
		request.Permissions = permissionsBody
	}

	return request, nil
}

type dashboardWorkspaceRequest struct {
	Attributes  dashboardWorkspaceAttributes `json:"attributes"`
	Permissions map[string]interface{}       `json:"permissions,omitempty"`
}

type dashboardWorkspaceAttributes struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Features    []string `json:"features,omitempty"`
}

type dashboardWorkspaceCreateResponse struct {
	Success bool `json:"success"`
	Result  struct {
		ID string `json:"id"`
	} `json:"result"`
	Error string `json:"error,omitempty"`
}

type dashboardWorkspaceGetResponse struct {
	Success bool               `json:"success"`
	Result  dashboardWorkspace `json:"result"`
	Error   string             `json:"error,omitempty"`
}

type dashboardWorkspaceBoolResponse struct {
	Success bool   `json:"success"`
	Result  bool   `json:"result"`
	Error   string `json:"error,omitempty"`
}

type dashboardWorkspace struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Features    []string `json:"features"`
}
