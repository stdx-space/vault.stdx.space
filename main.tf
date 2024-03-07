terraform {
  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "0.48.1"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "4.19.0"
    }
  }
}

variable "cloudflare_api_token" {
  type        = string
  sensitive   = true
  description = "Cloudflare API token for ACME"
}

variable "github_pat" {
  type        = string
  sensitive   = true
  description = "Github Personal Access Token for dispatching Vault unseal workflows"
}

variable "s3_secret_key" {
  type        = string
  sensitive   = true
  description = "S3 Secret Key for Vault storage"
}

provider "proxmox" {
  ssh {
    agent = false
    node {
      name    = "pve"
      address = "pve.stdx.space"
    }
  }
}

data "cloudflare_zone" "stdx_space" {
  name = "stdx.space"
}

locals {
  node            = "pve"
  network         = "10.101.0.0/16"
  ips             = ["10.101.101.2"]
  os_template_id  = "local:iso/flatcar_production_qemu_image.img"
  cluster_size    = 1
  authorized_keys = split("\n", data.http.ssh_pubkeys.response_body)
}

data "http" "ssh_pubkeys" {
  url = "https://github.com/STommydx.keys"
}

# module "pki" {
#   source              = "git::https://gitlab.com/narwhl/wip/blueprint.git//modules/pki"
#   root_ca_common_name = "STDXSPACE"
#   root_ca_org_name    = "Hashicorp"
#   root_ca_org_unit    = "Development"
#   extra_server_certificates = [
#     {
#       san_dns_names    = ["nomad.local"]
#       san_ip_addresses = local.ips
#     }
#   ]
# }

module "vault" {
  source          = "git::https://gitlab.com/narwhl/wip/blueprint.git//modules/vault-oss?ref=fix%2Fvault-sidecar"
  access_key      = "77beef5a54f85ae8c1f177351fe6d7f6"
  secret_key      = var.s3_secret_key
  s3_endpoint     = "https://c814d4c5591b582edf31951b0bd09497.r2.cloudflarestorage.com"
  bucket          = "vault-sandbox"
  acme_email      = "lab@stdx.space"
  acme_domain     = "vault.stdx.space"
  cf_zone_token   = var.cloudflare_api_token
  cf_dns_token    = var.cloudflare_api_token
  gh_access_token = var.github_pat
  gh_repository   = "stdx-space/vault.stdx.space"
}

module "flatcar" {
  source  = "git::https://gitlab.com/narwhl/wip/blueprint.git//modules/flatcar"
  name    = "vault"
  network = local.network
  # ca_certs = [
  #   module.pki.keychain.root_ca_cert,
  #   module.pki.keychain.intermediate_ca_cert
  # ]
  ip_address = local.ips[0]
  gateway_ip = cidrhost(local.network, 1)
  substrates = [
    module.vault.manifest,
  ]
  ssh_authorized_keys = local.authorized_keys
}

module "proxmox" {
  source              = "git::https://gitlab.com/narwhl/wip/blueprint.git//modules/proxmox"
  name                = "vault"
  node                = local.node
  vcpus               = 2
  memory              = 4096
  storage_pool        = "local-lvm"
  disk_size           = 48
  os_template_id      = local.os_template_id
  provisioning_config = module.flatcar.config
  networks = [
    {
      id = "vmbr1"
    }
  ]
}

resource "cloudflare_record" "vault" {
  zone_id = data.cloudflare_zone.stdx_space.id
  name    = "vault"
  type    = "A"
  value   = local.ips[0]
  proxied = false
}
