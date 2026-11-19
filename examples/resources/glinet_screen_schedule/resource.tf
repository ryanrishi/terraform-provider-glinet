# The screen is off from 23:00 to 06:30 on all days.
resource "glinet_screen_schedule" "this" {
  enabled       = true
  turn_off_time = "23:00"
  turn_on_time  = "06:30"
  days          = [0, 1, 2, 3, 4, 5, 6]
}
