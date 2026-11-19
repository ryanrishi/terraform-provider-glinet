# The switch turns the guest Wi-Fi on and off.
resource "glinet_switch_button" "this" {
  function     = "wifi"
  sub_function = "guest_wifi"
}
