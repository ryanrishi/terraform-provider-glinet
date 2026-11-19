resource "glinet_parental_control_ruleset" "homework" {
  name      = "homework"
  blacklist = ["games.example.com"]
}

# The group blocks nothing, uses the rule set "homework" from 16:00 to 18:00 on weekdays,
# and blocks all traffic from 21:00 to the end of the day.
resource "glinet_parental_control_group" "kids" {
  name            = "kids"
  default_ruleset = "accept"
  macs            = ["02:00:00:00:00:01", "02:00:00:00:00:02"]

  schedules = [
    {
      days    = [1, 2, 3, 4, 5]
      begin   = "16:00"
      end     = "18:00"
      ruleset = glinet_parental_control_ruleset.homework.id
    },
    {
      days    = [0, 1, 2, 3, 4, 5, 6]
      begin   = "21:00"
      end     = "24:00"
      ruleset = "drop"
    },
  ]
}
