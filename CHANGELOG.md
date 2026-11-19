# Changelog

## Unreleased

BREAKING CHANGES:

* `glinet_local_access`: On a router that does not return `ssh_enabled` (firmware 4.7.0), `ssh_enabled` is null in the state, not `false`, and a write sends no `ssh_enabled` when it is not set. Before, the write sent `ssh_enabled` `false`. When `ssh_enabled` is set and the router does not return it, the apply fails before the write.
* `glinet_firewall_dmz`: When the router reports no DMZ priority (before the first write), `higher_priority` is null in the state, not `false`, and a write sends no `priority` when `higher_priority` is not set. Before, the write sent `priority` `false`. The provider compares the priority after the write only when it sent a priority.
* `glinet_wireguard_server`: The provider sends `ipv6_enable` only when the router returned it and `address_v6` is not empty. Firmware 4.8.3 does not return `ipv6_enable`, so a write no longer sends `ipv6_enable` `false` there.
* Each call to the router has a time limit of 60 seconds. Before, a call had no time limit.

Attribute renames. Change the names in your configuration and in each reference to them. The values do not change, except for `tx_power`.

* `glinet_wan_access`: `https` is now `https_enabled`.
* `glinet_wan_access`: `ssh` is now `ssh_enabled`.
* `glinet_wan_access`: `ping` is now `ping_enabled`.
* `glinet_wan_access`: `whitelist_enabled` is now `allow_list_enabled`.
* `glinet_wan_access`: `whitelist` is now `allow_list`.
* `glinet_network_advanced`: `full_cone_nat` is now `full_cone_nat_enabled`.
* `glinet_network_advanced`: `sip_alg` is now `sip_alg_enabled`.
* `glinet_wireguard_server`: `local_access` is now `local_access_enabled`.
* `glinet_wireguard_server`: `client_to_client` is now `client_to_client_enabled`.
* `glinet_wireguard_server`: `masquerade` is now `masquerade_enabled`.
* `glinet_wan_port`: `name` is now `port`, as in `glinet_wan`.
* `glinet_wan_port`: `mac_address` is now `mac`.
* `glinet_wan_port`: `default_mac_address` is now `default_mac`.
* `glinet_wireguard_server_route`: `dest` is now `destination`. The import ID is still the destination network. See the note below about the state.
* `glinet_parental_control_group`: `start_time` and `end_time` replace `begin` and `end` in each item of `schedules`.
* `glinet_parental_control_ruleset`: `blacklist` is now `block_list`.
* `glinet_parental_control_ruleset`: `url` is now `block_list_url`.
* `glinet_system_timezone` (resource and data source): `zonename` is now `zone_name`.
* `glinet_system_timezone` (resource and data source) and data source `glinet_system_status`: `tzoffset` is now `timezone_offset`.
* `glinet_client_info` and data source `glinet_clients`: `class` is now `icon`.
* Data source `glinet_clients`: `limit_rx` is now `download_limit`, and `limit_tx` is now `upload_limit`.
* Data source `glinet_repeater_status`: `mac_address` is now `mac`.
* Data source `glinet_repeater_saved_networks`: `mac_address` is now `mac_clone`, and its attribute `period` is now `period_hours`.
* Data source `glinet_repeater_saved_networks`: `auto_portal` is now `auto_portal_enabled`, and `disguise` is now `disguise_enabled`.
* `glinet_dns`: `force_dns` is now `force_dns_enabled`.
* `glinet_dns`: `rebind_protection` is now `rebind_protection_enabled`.
* `glinet_dns`: `override_vpn` is now `override_vpn_enabled`.
* `glinet_lan`: `ap_isolation` is now `ap_isolation_enabled`.
* `glinet_lan`: `wan_isolation` is now `wan_isolation_enabled`.
* `glinet_local_access`: `redirect_https` is now `redirect_https_enabled`.
* `glinet_local_access`: `luci_redirect_https` is now `luci_redirect_https_enabled`.
* `glinet_parental_control`: `auto_update` is now `auto_update_enabled`.
* `glinet_parental_control`: `drop_anonymous` is now `drop_anonymous_enabled`.
* `glinet_wifi_radio`: `random_bssid` is now `random_bssid_enabled`.
* `glinet_wifi_radio`: `tx_power` takes `max`, `high`, `medium` and `low` in lower case. Before, it took `Max`, `High`, `Medium` and `Low`. The router still gets `Max`, `High`, `Medium` and `Low`.

The state of release 0.2.0: you do not have to change the state, except for `glinet_wireguard_server_route`. The schema version of the resources did not change, so the provider reads the old state with the new schema: it ignores each attribute that the new schema does not have, and a new attribute is null. The next refresh reads the new attributes from the router.

* `glinet_wireguard_server_route`: the provider finds the route rule by `destination`, which is null in the old state, so the refresh removes the resource from the state, and the next apply fails because the route rule exists on the router. Before the first plan with this release, run `terraform state rm <address>` and then `terraform import <address> <destination network>` for each route rule (or use an `import` block).
* `glinet_parental_control_ruleset`: when the router does not return the URL of the block list, the refresh keeps the value of the state, which is null after the rename. Then the first plan shows an update that sets `block_list_url`, and the apply sends the same URL again. It is not known whether the router returns the URL.

ENHANCEMENTS:

* `glinet_wireguard_server_route`: `metric` accepts values from 1 to 4294967295, as the web UI does. Before, the limit was 2147483647.
* `glinet_firewall_rule`: The error of a failed write to a rule with no destination port tells how to remove the rule with SSH and `uci` only for the fault of firmware 4.8.3 (HTTP status 500 from `set_rule` or `remove_rule`), not for other errors.
* `glinet_local_access`: When a write that moves the address of the API fails with an HTTP status, the error no longer says that the router did not answer.
* The client is `glinet-client-go` v1.0.0.

NOTES:

* After `terraform import`, the next plan can show an update in-place with no changed attribute when the configuration sets a sensitive attribute from a variable with `sensitive = true`. Terraform 1.5.7 does this; it is not known whether newer versions do. One `terraform apply` writes the sensitive mark into the state. The provider documentation tells what this apply does on the router.
* The client v1.0.0 refuses a 0 or 1 flag of the router with another value, for example `2` or `"1"`, in `glinet_lan` and `glinet_network_advanced`. Such a read now fails with an error.
* CI runs `go vet` for the 32-bit platforms `linux/386` and `linux/arm` (`make vet32`).

## 0.2.0 (2026-11-19)

This release adds 24 resources and 6 data sources. The provider now has 45 resources and 15 data sources. The provider now supports the options that the web UI of firmware 4.8.3 can set for each resource, not only the options of the test router.

Most new writes come from the web UI code of firmware 4.8.3 and are tested only on a fake router, not on a real router. The page of each resource states this. These writes were tested on a GL-AXT1800 with firmware 4.8.3: the fan temperature of `glinet_fan`, an allow-list entry of `glinet_client_allow` while the router is in block-list mode, and `glinet_firewall_rule` with IPv6 addresses, IPv4 and IPv6 CIDR blocks, the protocols `esp` and `icmpv6`, and a source MAC address in upper case.

BREAKING CHANGES:

* `glinet_clients`: `limit_rx` and `limit_tx` are decimal numbers (Float64) and no longer whole numbers (Int64), because the web UI can set a speed limit with a fraction. The unit is KiB/s.

FEATURES:

* **New resources, clients and access control:** `glinet_access_control` (the access control mode: `block_list` or `allow_list`), `glinet_client_allow` (one MAC address in the allow list), `glinet_client_info` (the name and the icon of a client), `glinet_client_speed_limit` (the download and upload limit of a client)
* **New resources, remote and local access:** `glinet_wan_access` (the access to the router from the WAN: HTTPS, SSH, ping and a whitelist), `glinet_local_access` (the ports of the web UI, LuCI and SSH, the redirect to HTTPS and the session timeout)
* **New resources, firewall:** `glinet_firewall_port_forward_order` (which port forwards come first, and in which order; firmware 4.8.3)
* **New resources, parental control:** `glinet_parental_control` (the global settings), `glinet_parental_control_group` (a group of clients with its default rule set and its weekly schedules), `glinet_parental_control_ruleset` (a rule set of apps and domains)
* **New resources, WAN and uplinks:** `glinet_wan_port` (the role and the MAC address of a wired WAN port), `glinet_multiwan` (failover or load balance), `glinet_multiwan_interface` (the tracking of an interface), `glinet_multiwan_sensitivity` (the sensitivity of the tracking), `glinet_repeater` (the switching mode and the band lock of the Wi-Fi uplink)
* **New resources, network and Wi-Fi:** `glinet_network_advanced` (Full Cone NAT and SIP ALG), `glinet_igmp_snooping`, `glinet_wifi_radio` (channel, channel width, mode, transmit power, random BSSID and the 6 GHz PSC option of a Wi-Fi radio)
* **New resources, hardware:** `glinet_fan` (the temperature at which the fan starts), `glinet_usb` (USB 3.0 or USB 2.0), `glinet_screen_schedule` (the schedule of the LCD screen), `glinet_switch_button` (the function of the side switch). On a router without the hardware, create and update fail and do not write to the router.
* **New resources, VPN:** `glinet_wireguard_server` (the configuration and the options of the WireGuard server; create initializes the server when it has no keys), `glinet_wireguard_server_route` (a route rule of the WireGuard server)
* **New data sources:** `glinet_fan_status`, `glinet_multiwan_status`, `glinet_parental_control_apps`, `glinet_repeater_status`, `glinet_repeater_saved_networks` (without the Wi-Fi keys and EAP identities), `glinet_tethering_status`

ENHANCEMENTS:

* `glinet_wan`: The new attribute `port` (`wan` or `secondwan`, default `wan`) manages the second wired WAN port of a dual-WAN router. The ID and the import ID are the port. The provider upgrades a state of 0.1.x, so the resource is not replaced.
* `glinet_wan_status`: The new attribute `second_wan` shows the state of the second wired WAN port.
* `glinet_firewall_dmz`: The new attribute `higher_priority` sets the DMZ priority. When it is not set, a write sends back the router value.
* `glinet_firewall_rule`: `source_ip` and `destination_ip` accept IPv4 and IPv6 addresses and CIDR blocks. `protocol` accepts each protocol name or number in lower case, for example `esp` or `icmpv6`, and no longer only `tcp`, `udp`, `tcp udp`, `icmp` and `all`. `source_mac` accepts upper case and mixed case, and the provider finds the rule without letter case.
* `glinet_firewall_rule`: The plan shows a warning when it creates, changes or destroys a rule with no destination port, because firmware 4.8.3 cannot change or remove such a rule. When a write to such a rule fails, the error tells how to remove the rule with SSH.
* `glinet_lan`: The new attributes `wan_isolation` ("block WAN" of the guest network, firmware 4.8.3) and `dhcp_lpr_servers` (0 to 5 IPv4 addresses). When they are not set, a write sends back the router values. `dhcp_lpr_servers = []` removes all LPR servers.
* `glinet_wifi_interface`: `encryption` accepts each value in the encryption list of the radio on the router. The provider checks the value against this list at apply time, before the write, and no longer against a fixed list of 5 values at plan time. A write of a main interface also sends back `chan_6g_only_psc` of a 6 GHz radio.
* `glinet_tailscale`: `exit_node_ip` accepts an IPv6 address.
* `glinet_wireguard_server_peer`: The new computed attribute `client_config` has the values for the configuration file of the client device. The provider reads it after create, update and import, not in each refresh.
* `glinet_lan_static_lease`, `glinet_client_block`: `mac` accepts upper case and mixed case. `glinet_zerotier`: `network_id` accepts upper-case hexadecimal digits. A different letter case on the router makes no plan diff. In `glinet_lan_static_lease` and `glinet_client_block`, a change of only the letter case of `mac` changes only the state and does not replace the resource. Import of `glinet_lan_static_lease` sets `mac` in the letter case of the router.
* `glinet_client_block`: The messages name the access control modes `block_list` and `allow_list`, as `glinet_access_control` does.
* `glinet_clients`: New attributes `ipv6`, `type`, `online_time`, `remote`, `rx`, `tx`, `total_rx`, `total_tx`, `total_rx_init`, `total_tx_init`, `last_rx`, `last_tx` and `last_update_rate`.
* `glinet_system_status`: New attributes `mode`, `timestamp`, `tzoffset`, `client_counts`, `services[].peer_id` and `group_id`, and `wifi_interfaces[].encryption`, `hidden` and `mld`.
* `glinet_system_info`: New attribute `hidden_features`. Data source `glinet_system_timezone`: new attribute `timestamp`.
* The client is `glinet-client-go` v0.13.0.

NOTES:

* The provider does not need Terraform 1.16, as the notes of 0.1.0 say. It uses version 6 of the plugin protocol, which Terraform supports from version 1.0, and no framework feature that needs a later version. CI tests Terraform 1.16 and 1.17 and the latest OpenTofu. Version 0.1.x of the provider is known to work with Terraform 1.5.7. Terraform before 1.3 does not show the plan warning of `glinet_firewall_rule` when it destroys a rule with no destination port.
* Firmware 4.8.3 has a fault: it cannot change or remove a firewall rule with no destination port (HTTP status 500). The router adds such a rule and the firewall uses it, but Terraform cannot update or destroy it on firmware 4.8.3. Then the only way to remove the rule is SSH on the router: the error of the failed write tells how.
* Allow-list mode blocks each client that is not in the allow list, also the computer that runs Terraform. `glinet_access_control` does not change to `allow_list` while the allow list is empty. `glinet_client_allow` also writes in block-list mode, so add the allow-list entries first and then change the mode.
* `glinet_wifi_radio`: A change of the channel or the channel width can restart the Wi-Fi of the router and can stop a repeater uplink on that radio.
* WireGuard resources: When `listen_port`, `persistent_keepalive` or `mtu` is not set, an update sends back the router value of the last read. When the router has no value, it sends `""`. It is not known what the router does with this value.
* Destroy of the new resources: `glinet_wan_access` disables the access from the WAN, and `glinet_parental_control` disables parental control. `glinet_client_allow`, `glinet_client_speed_limit`, `glinet_parental_control_group`, `glinet_parental_control_ruleset` and `glinet_wireguard_server_route` remove their item from the router. The other new resources leave the router unchanged.
* Not supported, with the reason:
  * Network mode: the router changes its address.
  * A resource that connects the repeater or USB tethering: the router has no call that saves a network without a connect, so a write would change the uplink at once.
  * Bark and the temporary rule set of parental control.
  * MLO networks of Wi-Fi 7 routers ([#127](https://github.com/ryanrishi/terraform-provider-glinet/issues/127)) and the cellular modem ([#128](https://github.com/ryanrishi/terraform-provider-glinet/issues/128)): the API results of such routers are necessary.
  * The WireGuard client groups of VPN service providers.
  * VPN policy, and the start and stop of VPN tunnels.
  * OpenVPN client and server.
  * The QoS configuration: no view of the web UI writes it.
  * The admin password.
* `make test` has a time limit of 20 minutes.

## 0.1.1 (2026-11-18)

FEATURES:

* **New data source:** `glinet_dns_secure_servers` (the encrypted DNS protocols and servers of the router)

ENHANCEMENTS:

* `glinet_dns`: The resource supports all DNS modes: `auto`, `manual`, `proxy` and `secure`. `servers` can be set in the modes `manual`, `proxy` and `secure`. The new attributes `protocol`, `dot_provider` and `dot_resolver_id` set encrypted DNS (DoT, DNSCrypt, DoH, oDoH).
* The client is `glinet-client-go` v0.11.0.

NOTES:

* `glinet_dns`: The writes of the modes `manual`, `proxy` and `secure` come from the web UI code of firmware 4.8.3. They are tested only on a fake router, not on a real router.

## 0.1.0 (2026-11-18)

This release is a full rework of the provider. Version 0.0.4 had one data source. Version 0.1.0 has 8 data sources and 21 resources, and each resource has import.

The provider is tested with a GL-AXT1800 on firmware 4.7.0 and 4.8.3. The schema can still change before 1.0.0.

BREAKING CHANGES:

* The data source `glinet_system_timezone_config` has the new name `glinet_system_timezone`.
* The provider needs Terraform 1.16 or later, or OpenTofu.

FEATURES:

* **New data sources:** `glinet_arp_entries`, `glinet_clients`, `glinet_firewall_zones`, `glinet_system_info`, `glinet_system_status`, `glinet_wan_status`, `glinet_wifi_status`
* **New resources, system:** `glinet_system_timezone`, `glinet_led`, `glinet_led_schedule`, `glinet_reboot_schedule`
* **New resources, LAN and clients:** `glinet_lan`, `glinet_lan_static_lease`, `glinet_client_block`
* **New resources, firewall:** `glinet_firewall_port_forward`, `glinet_firewall_dmz`, `glinet_firewall_rule`
* **New resources, DNS:** `glinet_dns`, `glinet_adguardhome`, `glinet_ddns`
* **New resources, network:** `glinet_wan`, `glinet_ipv6`, `glinet_wifi_interface`
* **New resources, VPN:** `glinet_tailscale`, `glinet_zerotier`, `glinet_wireguard_client_group`, `glinet_wireguard_client_config`, `glinet_wireguard_server_peer`

ENHANCEMENTS:

* The login works with the SHA-256 login hash of firmware 4.8.3.
* The client logs in again when the router session expires.

NOTES:

* The writes of these resources are tested only on a fake router, not on a real router: `glinet_wan`, `glinet_ipv6`, `glinet_lan`, `glinet_wifi_interface`, `glinet_wireguard_client_group`, `glinet_wireguard_client_config`, `glinet_wireguard_server_peer`. A write of `glinet_lan` or `glinet_wifi_interface` restarts the Wi-Fi of the router.
* Destroy disables a feature that exposes the router or acts on its own (DMZ, DDNS, Tailscale, ZeroTier, reboot schedule), removes a list item (static lease, client block, port forward, firewall rule), and leaves a plain setting unchanged. The documentation of each resource states its destroy behaviour.
* Firmware 4.8.3 replaces the VPN client status methods of `wg-client` with the module `vpn-client`. The provider does not use the removed methods. `glinet_system_status` shows no `wgclient` and `ovpnclient` service on that firmware.
