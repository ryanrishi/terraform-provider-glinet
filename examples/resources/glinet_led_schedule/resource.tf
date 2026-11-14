# The LEDs are off from 22:00 to 07:00 on weekdays.
resource "glinet_led_schedule" "this" {
  enabled       = true
  turn_off_time = "22:00"
  turn_on_time  = "07:00"
  days          = [1, 2, 3, 4, 5]
}
