# Changelog

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
