provider "opensearch" {
  url            = "http://127.0.0.1:9200"
  dashboards_url = "http://127.0.0.1:5601"
}

resource "opensearch_dashboard_data_source" "remote" {
  title       = "Remote cluster"
  description = "Remote OpenSearch cluster"
  endpoint    = "https://remote.example.com"
  tenant_name = "tenant-a"
  auth_type   = "no_auth"
}
