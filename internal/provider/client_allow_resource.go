package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/ryanrishi/glinet-client-go"
)

func NewClientAllowResource() resource.Resource {
	return &clientListResource{list: clientList{
		action: "allow",
		mode:   glinet.AccessControlAllowList,
		description: "Allows a client by its MAC address: the MAC address is an entry in the allow list of the router (`black_white_list`). " +
			"The router has one allow list for all clients, and uses it only in allow-list mode. " +
			"In allow-list mode, the router blocks each client that is not in the allow list.\n\n" +
			"~> **Warning:** in allow-list mode, destroy of an entry can block that client, also when it is the computer that runs Terraform.\n\n" +
			"The resource does not manage the access control mode (see `glinet_access_control`). " +
			"Create, update and destroy also work while the router is in block-list mode: the router then does not use the allow list, " +
			"and an entry has no effect until the mode changes to allow-list mode. " +
			"Thus you can add the allow-list entries first and then set `glinet_access_control` to `allow_list`, with `depends_on` or a reference. " +
			"In allow-list mode, destroy fails when the MAC address is the last entry of the allow list, because an empty allow list can block all clients. " +
			"In block-list mode, destroy also removes the last entry. " +
			"The provider writes the MAC address in upper case, as the web UI does, and finds entries without letter case. " +
			"The state keeps `mac` in the letter case of the configuration. Import sets `mac` in lower case. " +
			"Destroy removes the MAC address in each letter case. " +
			"An add and a remove of an entry in block-list mode were tested on a GL-AXT1800 with firmware 4.8.3: the router changed the allow list and kept block-list mode. " +
			"The other allow-list writes are from the web UI code of firmware 4.8.3 and were not tested on a router.",
		writesInOtherMode: true,
		add:               (*glinet.AccessControlService).AddAllowedMAC,
		remove:            (*glinet.AccessControlService).RemoveAllowedMAC,
		keepLastEntry:     true,
	}}
}
