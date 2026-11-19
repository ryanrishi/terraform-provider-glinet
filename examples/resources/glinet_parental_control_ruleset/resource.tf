data "glinet_parental_control_apps" "all" {}

# Blocks the game apps and two domains.
resource "glinet_parental_control_ruleset" "homework" {
  name      = "homework"
  color     = "#4b9bd9"
  apps      = [for app in data.glinet_parental_control_apps.all.apps : app.id if app.type == 2]
  blacklist = ["games.example.com", "videos.example.net"]
}

# Blocks the domains of a block list that the router loads.
resource "glinet_parental_control_ruleset" "night" {
  name = "night"
  url  = "https://blocklist.example.com/night.txt"
}
