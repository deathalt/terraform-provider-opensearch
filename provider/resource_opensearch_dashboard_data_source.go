package provider

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/olivere/elastic/uritemplates"
)

const (
	dashboardDataSourceSavedObjectType = "data-source"

	dashboardDataSourceAuthNoAuth           = "no_auth"
	dashboardDataSourceAuthUsernamePassword = "username_password"
	dashboardDataSourceAuthSigV4            = "sigv4"

	dashboardDataSourceSigV4ServiceOpenSearch           = "es"
	dashboardDataSourceSigV4ServiceOpenSearchServerless = "aoss"
)

func resourceOpensearchDashboardDataSource() *schema.Resource {
	return &schema.Resource{
		Description: "Provides an OpenSearch Dashboards data source connection resource.",
		Create:      resourceOpensearchDashboardDataSourceCreate,
		Read:        resourceOpensearchDashboardDataSourceRead,
		Update:      resourceOpensearchDashboardDataSourceUpdate,
		Delete:      resourceOpensearchDashboardDataSourceDelete,
		Schema: map[string]*schema.Schema{
			"title": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Data source connection title.",
			},
			"description": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Data source connection description.",
			},
			"endpoint": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "Data source connection endpoint URL.",
			},
			"tenant_name": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Default:     "global",
				Description: "OpenSearch Dashboards tenant name where the data source connection is stored. Defaults to the global tenant.",
			},
			"auth_type": {
				Type:         schema.TypeString,
				Required:     true,
				ValidateFunc: validation.StringInSlice([]string{dashboardDataSourceAuthNoAuth, dashboardDataSourceAuthUsernamePassword, dashboardDataSourceAuthSigV4}, false),
				Description:  "Authentication type. Supported values are no_auth, username_password, and sigv4.",
			},
			"username": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				Description: "Username for username_password authentication.",
			},
			"password": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				Description: "Password for username_password authentication.",
			},
			"aws_access_key": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				Description: "AWS access key for sigv4 authentication.",
			},
			"aws_secret_key": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				Description: "AWS secret key for sigv4 authentication.",
			},
			"aws_region": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "AWS region for sigv4 authentication.",
			},
			"aws_service": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      dashboardDataSourceSigV4ServiceOpenSearch,
				ValidateFunc: validation.StringInSlice([]string{dashboardDataSourceSigV4ServiceOpenSearch, dashboardDataSourceSigV4ServiceOpenSearchServerless}, false),
				Description:  "AWS SigV4 service name. Use es for OpenSearch or aoss for OpenSearch Serverless.",
			},
			"data_source_version": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Detected data source version.",
			},
			"data_source_engine_type": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Detected data source engine type.",
			},
			"installed_plugins": {
				Type:        schema.TypeSet,
				Computed:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Detected plugins installed on the data source.",
			},
		},
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
	}
}

func resourceOpensearchDashboardDataSourceCreate(d *schema.ResourceData, meta interface{}) error {
	attributes, err := dashboardDataSourceAttributes(d, true, true)
	if err != nil {
		return err
	}
	headers := dashboardDataSourceHeaders(d)

	if err := fetchDashboardDataSourceMetadata(meta.(*ProviderConf), "", attributes, headers); err != nil {
		return err
	}

	var response dashboardDataSourceSavedObjectResponse
	if err := dashboardsRequestWithHeaders(meta.(*ProviderConf), http.MethodPost, "/api/saved_objects/data-source", dashboardSavedObjectAttributesRequest{Attributes: attributes}, &response, headers); err != nil {
		return err
	}
	if response.ID == "" {
		return fmt.Errorf("unexpected data source create response: %+v", response)
	}

	d.SetId(response.ID)
	return resourceOpensearchDashboardDataSourceRead(d, meta)
}

func resourceOpensearchDashboardDataSourceRead(d *schema.ResourceData, meta interface{}) error {
	dataSource, err := getDashboardDataSource(d.Id(), meta, dashboardDataSourceHeaders(d))
	if err != nil {
		if errors.Is(err, errDashboardsNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}

	ds := &resourceDataSetter{d: d}
	ds.set("title", dataSource.Attributes.Title)
	ds.set("description", dataSource.Attributes.Description)
	ds.set("endpoint", dataSource.Attributes.Endpoint)
	ds.set("tenant_name", d.Get("tenant_name").(string))
	if dataSource.Attributes.Auth != nil {
		ds.set("auth_type", dataSource.Attributes.Auth.Type)
	}
	ds.set("data_source_version", dataSource.Attributes.DataSourceVersion)
	ds.set("data_source_engine_type", dataSource.Attributes.DataSourceEngineType)
	ds.set("installed_plugins", flattenStringSet(dataSource.Attributes.InstalledPlugins))

	if dataSource.Attributes.Auth != nil && dataSource.Attributes.Auth.Type == dashboardDataSourceAuthUsernamePassword {
		if username, ok := dataSource.Attributes.Auth.Credentials["username"].(string); ok {
			ds.set("username", username)
		}
	}
	if dataSource.Attributes.Auth != nil && dataSource.Attributes.Auth.Type == dashboardDataSourceAuthSigV4 {
		if region, ok := dataSource.Attributes.Auth.Credentials["region"].(string); ok {
			ds.set("aws_region", region)
		}
		if service, ok := dataSource.Attributes.Auth.Credentials["service"].(string); ok {
			ds.set("aws_service", service)
		}
	}

	return ds.err
}

func resourceOpensearchDashboardDataSourceUpdate(d *schema.ResourceData, meta interface{}) error {
	attributes, err := dashboardDataSourceAttributes(d, false, false)
	if err != nil {
		return err
	}
	headers := dashboardDataSourceHeaders(d)

	if dashboardDataSourceAuthChanged(d) {
		fullAttributes, err := dashboardDataSourceAttributes(d, true, true)
		if err != nil {
			return err
		}
		if err := fetchDashboardDataSourceMetadata(meta.(*ProviderConf), d.Id(), fullAttributes, headers); err != nil {
			return err
		}
		attributes.Auth = fullAttributes.Auth
		attributes.DataSourceVersion = fullAttributes.DataSourceVersion
		attributes.DataSourceEngineType = fullAttributes.DataSourceEngineType
		attributes.InstalledPlugins = fullAttributes.InstalledPlugins
	}

	path, err := dashboardSavedObjectPath(dashboardDataSourceSavedObjectType, d.Id())
	if err != nil {
		return err
	}

	var response dashboardDataSourceSavedObjectResponse
	if err := dashboardsRequestWithHeaders(meta.(*ProviderConf), http.MethodPut, path, dashboardSavedObjectAttributesRequest{Attributes: attributes}, &response, headers); err != nil {
		return err
	}

	return resourceOpensearchDashboardDataSourceRead(d, meta)
}

func resourceOpensearchDashboardDataSourceDelete(d *schema.ResourceData, meta interface{}) error {
	path, err := dashboardSavedObjectPath(dashboardDataSourceSavedObjectType, d.Id())
	if err != nil {
		return err
	}

	if err := dashboardsRequestWithHeaders(meta.(*ProviderConf), http.MethodDelete, path, nil, nil, dashboardDataSourceHeaders(d)); err != nil {
		if errors.Is(err, errDashboardsNotFound) {
			return nil
		}
		return err
	}
	return nil
}

func getDashboardDataSource(id string, meta interface{}, headers map[string]string) (*dashboardDataSourceSavedObjectResponse, error) {
	path, err := dashboardSavedObjectPath(dashboardDataSourceSavedObjectType, id)
	if err != nil {
		return nil, err
	}

	var response dashboardDataSourceSavedObjectResponse
	if err := dashboardsRequestWithHeaders(meta.(*ProviderConf), http.MethodGet, path, nil, &response, headers); err != nil {
		return nil, err
	}
	return &response, nil
}

func fetchDashboardDataSourceMetadata(conf *ProviderConf, id string, attributes *dashboardDataSourceAttributesBody, headers map[string]string) error {
	request := dashboardDataSourceMetadataRequest{
		ID: id,
		DataSourceAttr: dashboardDataSourceMetadataAttributes{
			Endpoint: attributes.Endpoint,
			Auth:     attributes.Auth,
		},
	}

	var response dashboardDataSourceMetadataResponse
	if err := dashboardsRequestWithHeaders(conf, http.MethodPost, "/internal/data-source-management/fetchDataSourceMetaData", request, &response, headers); err != nil {
		return err
	}

	attributes.DataSourceVersion = response.DataSourceVersion
	attributes.DataSourceEngineType = response.DataSourceEngineType
	attributes.InstalledPlugins = response.InstalledPlugins
	return nil
}

func dashboardDataSourceHeaders(d *schema.ResourceData) map[string]string {
	return dashboardTenantHeaders(d.Get("tenant_name").(string))
}

func dashboardDataSourceAttributes(d *schema.ResourceData, includeAuth bool, includeEndpoint bool) (*dashboardDataSourceAttributesBody, error) {
	attributes := &dashboardDataSourceAttributesBody{
		Title:       d.Get("title").(string),
		Description: d.Get("description").(string),
	}

	if includeEndpoint {
		attributes.Endpoint = strings.TrimSpace(d.Get("endpoint").(string))
	}

	if includeAuth {
		auth, err := dashboardDataSourceAuth(d)
		if err != nil {
			return nil, err
		}
		attributes.Auth = auth
	}

	return attributes, nil
}

func dashboardDataSourceAuth(d *schema.ResourceData) (*dashboardDataSourceAuthBody, error) {
	authType := d.Get("auth_type").(string)
	auth := &dashboardDataSourceAuthBody{
		Type:        authType,
		Credentials: map[string]interface{}{},
	}

	switch authType {
	case dashboardDataSourceAuthNoAuth:
		return auth, nil
	case dashboardDataSourceAuthUsernamePassword:
		username := d.Get("username").(string)
		password := d.Get("password").(string)
		if username == "" || password == "" {
			return nil, fmt.Errorf("username and password must be set when auth_type is username_password")
		}
		auth.Credentials["username"] = username
		auth.Credentials["password"] = password
	case dashboardDataSourceAuthSigV4:
		accessKey := d.Get("aws_access_key").(string)
		secretKey := d.Get("aws_secret_key").(string)
		region := d.Get("aws_region").(string)
		service := d.Get("aws_service").(string)
		if accessKey == "" || secretKey == "" || region == "" || service == "" {
			return nil, fmt.Errorf("aws_access_key, aws_secret_key, aws_region, and aws_service must be set when auth_type is sigv4")
		}
		auth.Credentials["accessKey"] = accessKey
		auth.Credentials["secretKey"] = secretKey
		auth.Credentials["region"] = region
		auth.Credentials["service"] = service
	default:
		return nil, fmt.Errorf("unsupported auth_type: %s", authType)
	}

	return auth, nil
}

func dashboardDataSourceAuthChanged(d *schema.ResourceData) bool {
	for _, key := range []string{"auth_type", "username", "password", "aws_access_key", "aws_secret_key", "aws_region", "aws_service"} {
		if d.HasChange(key) {
			return true
		}
	}
	return false
}

func dashboardSavedObjectPath(objectType, id string) (string, error) {
	path, err := uritemplates.Expand("/api/saved_objects/{type}/{id}", map[string]string{
		"type": objectType,
		"id":   id,
	})
	if err != nil {
		return "", fmt.Errorf("error building URL path for saved object: %+v", err)
	}
	return path, nil
}

type dashboardSavedObjectAttributesRequest struct {
	Attributes interface{} `json:"attributes"`
}

type dashboardDataSourceMetadataRequest struct {
	ID             string                                `json:"id,omitempty"`
	DataSourceAttr dashboardDataSourceMetadataAttributes `json:"dataSourceAttr"`
}

type dashboardDataSourceMetadataAttributes struct {
	Endpoint string                       `json:"endpoint"`
	Auth     *dashboardDataSourceAuthBody `json:"auth"`
}

type dashboardDataSourceMetadataResponse struct {
	DataSourceVersion    string   `json:"dataSourceVersion"`
	DataSourceEngineType string   `json:"dataSourceEngineType"`
	InstalledPlugins     []string `json:"installedPlugins"`
}

type dashboardDataSourceSavedObjectResponse struct {
	ID         string                            `json:"id"`
	Type       string                            `json:"type"`
	Attributes dashboardDataSourceAttributesBody `json:"attributes"`
}

type dashboardDataSourceAttributesBody struct {
	Title                string                       `json:"title"`
	Description          string                       `json:"description"`
	Endpoint             string                       `json:"endpoint,omitempty"`
	Auth                 *dashboardDataSourceAuthBody `json:"auth,omitempty"`
	DataSourceVersion    string                       `json:"dataSourceVersion,omitempty"`
	DataSourceEngineType string                       `json:"dataSourceEngineType,omitempty"`
	InstalledPlugins     []string                     `json:"installedPlugins,omitempty"`
}

type dashboardDataSourceAuthBody struct {
	Type        string                 `json:"type"`
	Credentials map[string]interface{} `json:"credentials"`
}
