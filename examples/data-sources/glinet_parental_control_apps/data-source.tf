data "glinet_parental_control_apps" "all" {}

# The IDs of the game apps (type 2).
output "game_app_ids" {
  value = [for app in data.glinet_parental_control_apps.all.apps : app.id if app.type == 2]
}
