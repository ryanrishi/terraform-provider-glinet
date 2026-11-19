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
			"The resource does not manage the access control mode (see `glinet_access_control`): " +
			"create and destroy fail when the router is not in allow-list mode, because the web UI writes to the allow list only in allow-list mode, " +
			"and it is not known what such a write does in block-list mode. " +
			"While the router is not in allow-list mode, refresh keeps the resource in the state and shows a warning. " +
			"Destroy fails when the MAC address is the last entry of the allow list, because an empty allow list can block all clients. " +
			"The provider writes the MAC address in upper case, as the web UI does, and finds entries without letter case. " +
			"The state keeps `mac` in the letter case of the configuration. Import sets `mac` in lower case. " +
			"Destroy removes the MAC address in each letter case. " +
			"The allow-list writes are from the web UI code of firmware 4.8.3 and were not tested on a router.",
		deleteOutsideModeAdvice: "Remove the resource from the configuration and then from the state with terraform state rm: " +
			"the entry then stays in the allow list of the router and is not managed. " +
			"Do not set allow-list mode only to remove an entry: allow-list mode can block clients.",
		add:           (*glinet.AccessControlService).AddAllowedMAC,
		remove:        (*glinet.AccessControlService).RemoveAllowedMAC,
		keepLastEntry: true,
	}}
}
