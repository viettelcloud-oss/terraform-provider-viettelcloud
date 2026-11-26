# Lookup Load Balancer L7 Rule by ID
data "viettelcloud_lb_l7_rule" "by_id" {
  l7_policy_id = "00000000-0000-0000-0000-000000000000"
  id           = "11111111-2222-3333-4444-555555555555"
}
