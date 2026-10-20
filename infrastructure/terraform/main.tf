# Storage adapter bounded-ext4-v1: the platform allocates the frozen integer-GiB asset; Terraform mounts the original volume.
terraform {
  required_version = "1.15.5"
  required_providers {
    coder = {
      source  = "coder/coder"
      version = "2.19.0"
    }
    docker = {
      source  = "kreuzwerker/docker"
      version = "4.6.0"
    }
  }
}

provider "coder" {}
provider "docker" { host = "unix:///var/run/docker.sock" }

variable "runtime_image" {
  type        = string
  description = "Preloaded, approved immutable runtime image ID"
  validation {
    condition     = can(regex("^sha256:[a-f0-9]{64}$", var.runtime_image))
    error_message = "An immutable locally verified image ID is required."
  }
}
variable "runtime_root" {
  type        = string
  description = "Controlled absolute host directory containing per-instance channels"
  validation {
    condition     = can(regex("^/[A-Za-z0-9/_.-]+$", var.runtime_root)) && !strcontains(var.runtime_root, "//") && !contains(split("/", var.runtime_root), "..") && !contains(split("/", var.runtime_root), ".")
    error_message = "A controlled absolute directory is required."
  }
}

data "coder_workspace" "current" {}
data "coder_workspace_owner" "current" {}
data "coder_parameter" "instance_id" {
  name    = "instance_id"
  type    = "string"
  mutable = false
  default = "00000000-0000-4000-8000-000000000001"
  validation {
    regex = "^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$"
    error = "A platform instance UUID is required."
  }
}
data "coder_parameter" "asset_id" {
  name    = "asset_id"
  type    = "string"
  mutable = false
  default = "00000000-0000-4000-8000-000000000002"
  validation {
    regex = "^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$"
    error = "An independently prepared original asset UUID is required."
  }
}

locals {
  instance_id = data.coder_parameter.instance_id.value
  asset_id    = data.coder_parameter.asset_id.value
  channel     = "${var.runtime_root}/${local.instance_id}"
}

resource "docker_network" "instance" {
  count  = data.coder_workspace.current.start_count
  name   = "hc-${local.instance_id}"
  driver = "bridge"
  ipv6   = false
  options = {
    "com.docker.network.bridge.name"       = "hci-${substr(replace(local.instance_id, "-", ""), 0, 11)}"
    "com.docker.network.bridge.enable_icc" = "false"
  }
  labels {
    label = "hc.instance_id"
    value = local.instance_id
  }
}

# Terraform owns only stopped compute and the per-instance network. The trusted
# Worker verifies the original filesystem, installs network fences, and applies
# the 256 PID limit before starting compute. No agent or coder_app is published.
resource "docker_container" "runtime" {
  count                 = data.coder_workspace.current.start_count
  name                  = "hc-${local.instance_id}"
  image                 = var.runtime_image
  start                 = false
  must_run              = false
  restart               = "no"
  destroy_grace_seconds = 30
  stop_timeout          = 30
  stop_signal           = "SIGTERM"
  user                  = "1000:1000"
  read_only             = true
  cpus                  = "2"
  memory                = 4096
  memory_swap           = 4096
  security_opts         = ["no-new-privileges:true"]
  capabilities { drop = ["ALL"] }
  tmpfs = { "/tmp" = "rw,noexec,nosuid,nodev,size=256m,uid=1000,gid=1000" }
  networks_advanced { name = docker_network.instance[0].name }
  mounts {
    target = "/data"
    source = "hc-${local.asset_id}"
    type   = "volume"
    volume_options { no_copy = true }
  }
  mounts {
    target = "/run/harness"
    source = local.channel
    type   = "bind"
  }
  mounts {
    target    = "/run/secrets/model-key"
    source    = "${local.channel}/model-key"
    type      = "bind"
    read_only = true
  }
  labels {
    label = "hc.instance_id"
    value = local.instance_id
  }
  labels {
    label = "hc.asset_id"
    value = local.asset_id
  }
  labels {
    label = "hc.workspace_owner_id"
    value = data.coder_workspace_owner.current.id
  }
  labels {
    label = "hc.workspace_id"
    value = data.coder_workspace.current.id
  }
}
