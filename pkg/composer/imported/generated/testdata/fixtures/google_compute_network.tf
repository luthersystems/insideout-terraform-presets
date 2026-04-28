name                            = "io-buqiks112yag-vpc"
auto_create_subnetworks         = false
routing_mode                    = "REGIONAL"
project                         = google_project.main.project_id
delete_default_routes_on_create = null
description                     = "Primary VPC"
mtu                             = 1460
