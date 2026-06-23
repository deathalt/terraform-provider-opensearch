package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	elastic7 "github.com/olivere/elastic/v7"
)

func resourceOpensearchDashboardWorkspaceObjects() *schema.Resource {
	return &schema.Resource{
		Description: "Associates existing saved objects with an OpenSearch Dashboards workspace.",
		Create:      resourceOpensearchDashboardWorkspaceObjectsCreate,
		Read:        resourceOpensearchDashboardWorkspaceObjectsRead,
		Update:      resourceOpensearchDashboardWorkspaceObjectsUpdate,
		Delete:      resourceOpensearchDashboardWorkspaceObjectsDelete,
		Schema: map[string]*schema.Schema{
			"workspace_id": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "Workspace ID to associate saved objects with.",
			},
			"tenant_name": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Default:     "global",
				Description: "OpenSearch Dashboards tenant name where the workspace and saved objects are stored. Defaults to the global tenant.",
			},
			"saved_object": {
				Type:        schema.TypeSet,
				Required:    true,
				MinItems:    1,
				Elem:        dashboardWorkspaceSavedObjectSchema(),
				Description: "Saved objects to associate with the workspace.",
			},
		},
	}
}

func dashboardWorkspaceSavedObjectSchema() *schema.Resource {
	return &schema.Resource{
		Schema: map[string]*schema.Schema{
			"type": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Saved object type, such as index-pattern, config, or dashboard.",
			},
			"id": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Saved object ID.",
			},
		},
	}
}

func resourceOpensearchDashboardWorkspaceObjectsCreate(d *schema.ResourceData, meta interface{}) error {
	if _, err := getDashboardWorkspace(d.Get("workspace_id").(string), meta, dashboardWorkspaceObjectsHeaders(d)); err != nil {
		return err
	}
	if err := ensureDashboardWorkspaceMapping(d, meta); err != nil {
		return err
	}
	if err := associateDashboardWorkspaceObjects(d, meta, "/api/workspaces/_associate"); err != nil {
		return err
	}
	d.SetId(d.Get("workspace_id").(string))
	return resourceOpensearchDashboardWorkspaceObjectsRead(d, meta)
}

func resourceOpensearchDashboardWorkspaceObjectsRead(d *schema.ResourceData, meta interface{}) error {
	_, err := getDashboardWorkspace(d.Get("workspace_id").(string), meta, dashboardWorkspaceObjectsHeaders(d))
	if err != nil {
		if errors.Is(err, errDashboardsNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}
	return nil
}

func resourceOpensearchDashboardWorkspaceObjectsUpdate(d *schema.ResourceData, meta interface{}) error {
	if err := ensureDashboardWorkspaceMapping(d, meta); err != nil {
		return err
	}

	oldObjects, newObjects := d.GetChange("saved_object")
	oldSet := oldObjects.(*schema.Set)
	newSet := newObjects.(*schema.Set)

	removed := oldSet.Difference(newSet)
	if removed.Len() > 0 {
		if err := dashboardWorkspaceObjectsRequest(meta.(*ProviderConf), "/api/workspaces/_dissociate", d.Get("workspace_id").(string), removed, dashboardWorkspaceObjectsHeaders(d)); err != nil {
			return err
		}
	}

	added := newSet.Difference(oldSet)
	if added.Len() > 0 {
		if err := dashboardWorkspaceObjectsRequest(meta.(*ProviderConf), "/api/workspaces/_associate", d.Get("workspace_id").(string), added, dashboardWorkspaceObjectsHeaders(d)); err != nil {
			return err
		}
	}

	return resourceOpensearchDashboardWorkspaceObjectsRead(d, meta)
}

func resourceOpensearchDashboardWorkspaceObjectsDelete(d *schema.ResourceData, meta interface{}) error {
	if err := associateDashboardWorkspaceObjects(d, meta, "/api/workspaces/_dissociate"); err != nil {
		if errors.Is(err, errDashboardsNotFound) {
			return nil
		}
		return err
	}
	return nil
}

func associateDashboardWorkspaceObjects(d *schema.ResourceData, meta interface{}, path string) error {
	return dashboardWorkspaceObjectsRequest(
		meta.(*ProviderConf),
		path,
		d.Get("workspace_id").(string),
		d.Get("saved_object").(*schema.Set),
		dashboardWorkspaceObjectsHeaders(d),
	)
}

func dashboardWorkspaceObjectsRequest(conf *ProviderConf, path, workspaceID string, objects *schema.Set, headers map[string]string) error {
	request := dashboardWorkspaceObjectsRequestBody{
		WorkspaceID:  workspaceID,
		SavedObjects: expandDashboardWorkspaceSavedObjects(objects),
	}

	var response dashboardWorkspaceObjectsResponse
	if err := dashboardsRequestWithHeaders(conf, http.MethodPost, path, request, &response, headers); err != nil {
		return err
	}
	if !response.Success {
		if response.Error != "" {
			return fmt.Errorf("workspace saved objects request failed: %s", response.Error)
		}
		return fmt.Errorf("unexpected workspace saved objects response: %+v", response)
	}
	for _, result := range response.Result {
		if result.Error != "" {
			return fmt.Errorf("workspace saved object %s association failed: %s", result.ID, result.Error)
		}
	}
	return nil
}

func dashboardWorkspaceObjectsHeaders(d *schema.ResourceData) map[string]string {
	return dashboardTenantHeaders(d.Get("tenant_name").(string))
}

func ensureDashboardWorkspaceMapping(d *schema.ResourceData, meta interface{}) error {
	return ensureDashboardWorkspaceIndexMapping(meta.(*ProviderConf), d.Get("tenant_name").(string))
}

func ensureDashboardWorkspaceIndexMapping(conf *ProviderConf, tenantName string) error {
	index := ".kibana"
	var err error
	if tenantName != "" && tenantName != "global" {
		index, err = resourceOpensearchOpenDistroDashboardComputeIndex(tenantName)
		if err != nil {
			return fmt.Errorf("could not compute tenant name: %+v", err)
		}
	}

	client, err := getClient(conf)
	if err != nil {
		return err
	}

	headers := http.Header{}
	if tenantName != "" && tenantName != "global" {
		headers.Set(SECURITY_TENANT_HEADER, tenantName)
	}

	_, err = client.PerformRequest(context.TODO(), elastic7.PerformRequestOptions{
		Method:      http.MethodPut,
		Path:        fmt.Sprintf("/%s/_mapping", index),
		Body:        `{"properties":{"workspaces":{"type":"keyword"}}}`,
		ContentType: "application/json",
		Headers:     headers,
	})
	if err != nil {
		return fmt.Errorf("failed to ensure dashboard workspace mapping for index %s: %+v", index, err)
	}
	return nil
}

func expandDashboardWorkspaceSavedObjects(objects *schema.Set) []dashboardWorkspaceSavedObject {
	savedObjects := make([]dashboardWorkspaceSavedObject, 0, objects.Len())
	for _, item := range objects.List() {
		object := item.(map[string]interface{})
		savedObjects = append(savedObjects, dashboardWorkspaceSavedObject{
			Type: object["type"].(string),
			ID:   object["id"].(string),
		})
	}
	return savedObjects
}

type dashboardWorkspaceObjectsRequestBody struct {
	WorkspaceID  string                          `json:"workspaceId"`
	SavedObjects []dashboardWorkspaceSavedObject `json:"savedObjects"`
}

type dashboardWorkspaceSavedObject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type dashboardWorkspaceObjectsResponse struct {
	Success bool                             `json:"success"`
	Result  []dashboardWorkspaceObjectResult `json:"result"`
	Error   string                           `json:"error,omitempty"`
}

type dashboardWorkspaceObjectResult struct {
	ID    string `json:"id"`
	Error string `json:"error,omitempty"`
}
