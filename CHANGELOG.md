# Changelog

## Unreleased

The provider now supports the options that the web UI of firmware 4.8.3 can set for each resource, not only the options of the test router. The new writes come from the web UI code and are tested only on a fake router, not on a real router.

FEATURES:

* **New resource:** `glinet_wifi_radio` (channel, channel width, mode, transmit power, random BSSID and the 6 GHz PSC option of a Wi-Fi radio)
* **New resource:** `glinet_access_control` (the access control mode: `block_list` or `allow_list`)
* **New resource:** `glinet_client_allow` (one MAC address in the allow list)
* **New resource:** `glinet_firewall_port_forward_order` (which port forwards come first, and in which order; firmware 4.8.3)

ENHANCEMENTS:

* `glinet_wan`: The new attribute `port` (`wan` or `secondwan`, default `wan`) manages the second wired WAN port of a dual-WAN router. The ID and the import ID are the port. The provider upgrades a state of 0.1.x, so the resource is not replaced.
* `glinet_wan_status`: The new attribute `second_wan` shows the state of the second wired WAN port.
* `glinet_firewall_dmz`: The new attribute `higher_priority` sets the DMZ priority. When it is not set, a write sends back the router value.
* `glinet_lan`: The new attributes `wan_isolation` ("block WAN" of the guest network, firmware 4.8.3) and `dhcp_lpr_servers` (0 to 5 IPv4 addresses). When they are not set, a write sends back the router values.
* `glinet_wireguard_server_peer`: The new computed attribute `client_config` has the values for the configuration file of the client device. The provider reads it after create, update and import, not in each refresh.
* `glinet_lan_static_lease`, `glinet_client_block`: `mac` accepts upper case and mixed case. `glinet_zerotier`: `network_id` accepts upper-case hexadecimal digits. A different letter case on the router makes no plan diff.
* `glinet_clients`: New attributes `ipv6`, `type`, `online_time`, `remote`, `rx`, `tx`, `total_rx`, `total_tx`, `total_rx_init`, `total_tx_init`, `last_rx`, `last_tx` and `last_update_rate`.
* `glinet_system_status`: New attributes `mode`, `timestamp`, `tzoffset`, `client_counts`, `services[].peer_id` and `group_id`, and `wifi_interfaces[].encryption`, `hidden` and `mld`.
* `glinet_system_info`: New attribute `hidden_features`. Data source `glinet_system_timezone`: new attribute `timestamp`.
* `glinet_wifi_interface`: A write of a main interface also sends back `chan_6g_only_psc` of a 6 GHz radio.
* The client is `glinet-client-go` v0.12.1.

NOTES:

* Allow-list mode blocks each client that is not in the allow list, also the computer that runs Terraform. `glinet_access_control` does not change to `allow_list` while the allow list is empty, and `glinet_client_allow` writes only in allow-list mode. Thus the first allow-list entry must come from the web UI of the router.
* `glinet_wifi_radio`: A change of the channel or the channel width can restart the Wi-Fi of the router and can stop a repeater uplink on that radio.
* WireGuard resources: An update sends `""` for a `listen_port`, `persistent_keepalive` or `mtu` that is not set and that the router does not have. It is not known what the router does with this value.
* Not supported: MLO networks of Wi-Fi 7 routers and the WireGuard client groups of VPN service providers. The web UI code does not show their full parameters.

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
