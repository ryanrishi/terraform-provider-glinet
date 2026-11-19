package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/ryanrishi/glinet-client-go"
)

func NewClientBlockResource() resource.Resource {
	return &clientListResource{list: clientList{
		action: "block",
		mode:   glinet.AccessControlBlockList,
		description: "Blocks a client by its MAC address: the MAC address is an entry in the block list of the router (`black_white_list`). " +
			"The router has one block list for all clients, and uses it only in block-list mode. " +
			"The resource does not manage the access control mode (see `glinet_access_control`): " +
			"create and destroy fail when the router is not in block-list mode, because it is not known what a write to the block list does in allow-list mode. " +
			"While the router is not in block-list mode, refresh keeps the resource in the state and shows a warning. " +
			"The provider writes the MAC address in upper case, as the web UI does, and finds entries without letter case. " +
			"The state keeps `mac` in the letter case of the configuration. Import sets `mac` in lower case. " +
			"Destroy removes the MAC address in each letter case. " +
			"It was not verified that the router blocks the traffic of a client in the list.",
		deleteOutsideModeAdvice: "Set the access control mode to \"block_list\" first, for example with glinet_access_control, " +
			"or remove the resource from the configuration and then from the state with terraform state rm: " +
			"the entry then stays on the router and is not managed.",
		add:    (*glinet.AccessControlService).AddBlockedMAC,
		remove: (*glinet.AccessControlService).RemoveBlockedMAC,
	}}
}
