"""Prepare the local model gateway's network independently of workspace compute."""
import ipaddress
import json
import subprocess


NETWORK = "hc-model-gateway"
SUBNET = "10.51.0.0/24"
GATEWAY = "10.51.0.1"


class GatewayNetworkError(RuntimeError):
    pass


def command(*args):
    try:
        result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
    except (OSError, subprocess.TimeoutExpired):
        raise GatewayNetworkError(
            "Model gateway network prerequisite failed: Docker and ip must be available locally."
        ) from None
    if result.returncode:
        raise GatewayNetworkError(
            "Model gateway network prerequisite failed: "
            + (
                "Docker network command failed."
                if args[0] == "docker"
                else "Cannot inspect host IPv4 addresses."
            )
        )
    return result.stdout


def _list(raw):
    try:
        result = json.loads(raw)
    except (ValueError, UnicodeError):
        raise GatewayNetworkError(
            "Model gateway network prerequisite returned invalid metadata."
        ) from None
    if not isinstance(result, list) or any(not isinstance(item, dict) for item in result):
        raise GatewayNetworkError("Model gateway network prerequisite returned invalid metadata.")
    return result


def _validate(network):
    ipam, labels = network.get("IPAM"), network.get("Labels")
    configs = ipam.get("Config") if isinstance(ipam, dict) else None
    if (
        network.get("Name") != NETWORK
        or network.get("Driver") != "bridge"
        or not isinstance(labels, dict)
        or labels.get("hc.model.gateway") != "local"
        or not isinstance(configs, list)
        or len(configs) != 1
        or not isinstance(configs[0], dict)
        or configs[0].get("Subnet") != SUBNET
        or configs[0].get("Gateway") != GATEWAY
    ):
        raise GatewayNetworkError(
            "Existing hc-model-gateway network has conflicting driver, label, subnet or gateway; it was left unchanged."
        )
    options = network.get("Options") or {}
    if not isinstance(options, dict):
        raise GatewayNetworkError(
            "Cannot determine the model gateway host bridge; the network was left unchanged."
        )
    bridge = options.get("com.docker.network.bridge.name")
    if not bridge:
        identity = network.get("Id")
        if (
            not isinstance(identity, str)
            or len(identity) != 64
            or any(character not in "0123456789abcdef" for character in identity)
        ):
            raise GatewayNetworkError(
                "Cannot determine the model gateway host bridge; the network was left unchanged."
            )
        bridge = "br-" + identity[:12]
    if (
        not isinstance(bridge, str)
        or not 1 <= len(bridge) <= 15
        or any(
            character not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-"
            for character in bridge
        )
    ):
        raise GatewayNetworkError(
            "Cannot determine the model gateway host bridge; the network was left unchanged."
        )
    return bridge


def ensure_network(run=command):
    names = run("docker", "network", "ls", "-q").decode().split()
    networks = _list(run("docker", "network", "inspect", *names)) if names else []
    existing = next((network for network in networks if network.get("Name") == NETWORK), None)
    if existing is not None:
        bridge = _validate(existing)
    else:
        for network in networks:
            ipam = network.get("IPAM")
            if not isinstance(ipam, dict) or not isinstance(ipam.get("Config") or [], list):
                raise GatewayNetworkError(
                    "Cannot validate existing Docker network subnets; no network was changed."
                )
            for config in ipam.get("Config") or []:
                try:
                    subnet = ipaddress.ip_network(config.get("Subnet"), strict=True)
                except (AttributeError, TypeError, ValueError):
                    raise GatewayNetworkError(
                        "Cannot validate existing Docker network subnets; no network was changed."
                    ) from None
                if ipaddress.ip_network(SUBNET).overlaps(subnet):
                    raise GatewayNetworkError(
                        "Model gateway subnet 10.51.0.0/24 overlaps another Docker network; no network was changed."
                    )
        try:
            run(
                "docker",
                "network",
                "create",
                "--driver",
                "bridge",
                "--subnet",
                SUBNET,
                "--gateway",
                GATEWAY,
                "--label",
                "hc.model.gateway=local",
                NETWORK,
            )
        except GatewayNetworkError as create_error:
            # Another initializer may have created it after our first listing.
            # Adopt only the same strictly validated network, never replace it.
            try:
                created = _list(run("docker", "network", "inspect", NETWORK))
            except GatewayNetworkError:
                raise create_error from None
        else:
            created = _list(run("docker", "network", "inspect", NETWORK))
        if len(created) != 1:
            raise GatewayNetworkError("Cannot confirm the created model gateway network.")
        bridge = _validate(created[0])
    addresses = _list(run("ip", "-j", "-4", "address", "show"))
    ready = any(
        interface.get("ifname") == bridge
        and isinstance(interface.get("flags"), list)
        and "UP" in interface["flags"]
        and isinstance(interface.get("addr_info"), list)
        and any(
            address.get("family") == "inet" and address.get("local") == GATEWAY
            for address in interface["addr_info"]
            if isinstance(address, dict)
        )
        for interface in addresses
    )
    if not ready:
        raise GatewayNetworkError(
            "hc-model-gateway exists, but host bridge "
            + bridge
            + " does not have ready IPv4 address 10.51.0.1. "
            "Restore its host bridge address before starting the model gateway; the network was not removed."
        )


if __name__ == "__main__":
    try:
        ensure_network()
    except GatewayNetworkError as error:
        raise SystemExit(str(error)) from None
