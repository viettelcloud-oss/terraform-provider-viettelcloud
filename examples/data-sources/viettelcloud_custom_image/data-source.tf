data "viettelcloud_custom_image" "example" {
  name   = "my-custom-image"
  region = "vn-central-1"
  status = "active"
  state  = "enabled"
}
output "custom_image_id" {
  value = data.viettelcloud_custom_image.example.id
}
