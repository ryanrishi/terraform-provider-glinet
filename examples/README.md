# Examples

`make generate` runs `terraform fmt` on this directory and then [tfplugindocs](https://github.com/hashicorp/terraform-plugin-docs), which puts these files into the pages in `docs/`:

| File | Page |
|---|---|
| `provider/provider.tf` | Provider page (`docs/index.md`) |
| `data-sources/<name>/data-source.tf` | Data source page, example |
| `resources/<name>/resource.tf` | Resource page, example |
| `resources/<name>/import.sh` | Resource page, import command |

`<name>` is the full name with the `glinet_` prefix, for example `resources/glinet_lan_static_lease/resource.tf`.
tfplugindocs ignores other files.
