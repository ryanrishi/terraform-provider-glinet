# The fan starts when the CPU temperature is more than 75 °C.
resource "glinet_fan" "this" {
  temperature = 75
}
