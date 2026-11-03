import copy
import hashlib
import json
import subprocess
import unittest
from types import SimpleNamespace
from unittest.mock import patch

from gateway_network import GATEWAY, NETWORK, SUBNET, GatewayNetworkError, command, ensure_network


def network(name=NETWORK, subnet=SUBNET):
    return {
        "Name": name,
        "Id": hashlib.sha256(name.encode()).hexdigest(),
        "Driver": "bridge",
        "Labels": {"hc.model.gateway": "local"},
        "IPAM": {"Config": [{"Subnet": subnet, "Gateway": GATEWAY}]},
    }


class FakeDocker:
    def __init__(self, networks=(), ready=True):
        self.networks = copy.deepcopy(list(networks))
        self.addresses = (
            [
                {
                    "ifname": "br-" + network()["Id"][:12],
                    "flags": ["UP", "NO-CARRIER"],
                    "addr_info": [{"family": "inet", "local": GATEWAY, "prefixlen": 24}],
                }
            ]
            if ready
            else []
        )
        self.calls = []

    def __call__(self, *args):
        self.calls.append(args)
        if args == ("docker", "network", "ls", "-q"):
            return "\n".join(item["Id"] for item in self.networks).encode()
        if args[:3] == ("docker", "network", "inspect"):
            selected = [
                item for item in self.networks if item["Name"] in args[3:] or item["Id"] in args[3:]
            ]
            return json.dumps(selected).encode()
        if args[:3] == ("docker", "network", "create"):
            assert args == (
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
            self.networks.append(network())
            return (NETWORK + "-id").encode()
        if args == ("ip", "-j", "-4", "address", "show"):
            return json.dumps(self.addresses).encode()
        raise AssertionError("Unexpected network operation: " + repr(args))


class GatewayNetwork(unittest.TestCase):
    def test_missing_network_is_created_with_explicit_gateway_and_rechecked(self):
        docker = FakeDocker([network("bridge", "172.17.0.0/16"), network("other", "172.18.0.0/16")])
        ensure_network(docker)
        self.assertEqual(
            [call[:3] for call in docker.calls],
            [
                ("docker", "network", "ls"),
                ("docker", "network", "inspect"),
                ("docker", "network", "create"),
                ("docker", "network", "inspect"),
                ("ip", "-j", "-4"),
            ],
        )
        self.assertEqual(docker.networks[-1]["Name"], NETWORK)
        self.assertEqual(docker.networks[-1]["IPAM"]["Config"][0]["Gateway"], GATEWAY)

    def test_existing_network_and_no_workspaces_is_idempotent(self):
        docker = FakeDocker([network()])
        original = copy.deepcopy(docker.networks)
        ensure_network(docker)
        ensure_network(docker)
        self.assertEqual(docker.networks, original)
        self.assertTrue(all(call[:3] != ("docker", "network", "create") for call in docker.calls))
        self.assertEqual(docker.calls.count(("ip", "-j", "-4", "address", "show")), 2)

    def test_explicit_bridge_name_is_supported_and_unrelated_interface_cannot_supply_address(self):
        fixture = network()
        fixture["Options"] = {"com.docker.network.bridge.name": "hc-model-gw"}
        docker = FakeDocker([fixture])
        docker.addresses[0]["ifname"] = "hc-model-gw"
        ensure_network(docker)
        docker.addresses[0]["ifname"] = "hci-workspace"
        with self.assertRaisesRegex(
            GatewayNetworkError,
            "host bridge hc-model-gw does not have ready IPv4 address 10.51.0.1",
        ):
            ensure_network(docker)
        self.assertEqual(docker.networks, [fixture])

    def test_concurrent_create_adopts_only_a_valid_gateway_network(self):
        for conflicting in [False, True]:
            docker = FakeDocker()

            def run(*args):
                if args[:3] == ("docker", "network", "create"):
                    docker.calls.append(args)
                    fixture = network()
                    if conflicting:
                        fixture["IPAM"]["Config"][0]["Gateway"] = "10.51.0.254"
                    docker.networks.append(fixture)
                    raise GatewayNetworkError("Synthetic concurrent create conflict")
                return docker(*args)

            if conflicting:
                with self.assertRaisesRegex(GatewayNetworkError, "conflicting.*left unchanged"):
                    ensure_network(run)
            else:
                ensure_network(run)
            self.assertEqual(len(docker.networks), 1)
            self.assertFalse(any("rm" in call or "prune" in call for call in docker.calls))

    def test_conflicting_network_is_never_changed(self):
        for field, value in [
            ("Driver", "overlay"),
            ("Labels", {"hc.model.gateway": "another"}),
            ("Labels", None),
            ("IPAM", {"Config": [{"Subnet": "10.52.0.0/24", "Gateway": GATEWAY}]}),
            ("IPAM", {"Config": [{"Subnet": SUBNET, "Gateway": "10.51.0.254"}]}),
            ("IPAM", {"Config": []}),
            ("IPAM", None),
        ]:
            fixture = network()
            fixture[field] = value
            docker = FakeDocker([fixture])
            original = copy.deepcopy(docker.networks)
            with self.subTest(field=field, value=value), self.assertRaisesRegex(
                GatewayNetworkError, "conflicting.*left unchanged"
            ):
                ensure_network(docker)
            self.assertEqual(docker.networks, original)
            self.assertEqual(len(docker.calls), 2)

    def test_overlapping_subnet_fails_before_creation(self):
        for subnet in ["10.51.0.0/24", "10.51.0.0/16", "10.51.0.128/25"]:
            docker = FakeDocker([network("other", subnet)])
            with self.subTest(subnet=subnet), self.assertRaisesRegex(
                GatewayNetworkError, "overlaps.*no network was changed"
            ):
                ensure_network(docker)
            self.assertEqual(len(docker.networks), 1)
            self.assertEqual(len(docker.calls), 2)

    def test_missing_or_down_host_address_reports_actionable_error_without_removal(self):
        bridge = "br-" + network()["Id"][:12]
        for addresses in [
            [],
            [
                {
                    "ifname": bridge,
                    "flags": ["UP"],
                    "addr_info": [{"family": "inet", "local": "10.52.0.1"}],
                }
            ],
            [{"ifname": bridge, "flags": [], "addr_info": [{"family": "inet", "local": GATEWAY}]}],
            [{"ifname": bridge, "flags": ["UP"], "addr_info": None}],
        ]:
            docker = FakeDocker([network()])
            docker.addresses = addresses
            original = copy.deepcopy(docker.networks)
            with self.subTest(addresses=addresses), self.assertRaisesRegex(
                GatewayNetworkError,
                "does not have ready IPv4 address 10.51.0.1.*Restore its host bridge address",
            ):
                ensure_network(docker)
            self.assertEqual(docker.networks, original)
            self.assertFalse(any("rm" in call or "prune" in call for call in docker.calls))

    def test_new_network_without_host_address_is_not_deleted(self):
        docker = FakeDocker(ready=False)
        with self.assertRaisesRegex(
            GatewayNetworkError, "does not have ready IPv4 address 10.51.0.1"
        ):
            ensure_network(docker)
        self.assertEqual([item["Name"] for item in docker.networks], [NETWORK])
        self.assertFalse(any("rm" in call or "prune" in call for call in docker.calls))

    def test_invalid_metadata_fails_closed(self):
        for raw in [b"not-json", b"{}", b"[null]"]:

            def run(*args):
                return b"synthetic-id" if args == ("docker", "network", "ls", "-q") else raw

            with self.subTest(raw=raw), self.assertRaisesRegex(
                GatewayNetworkError, "invalid metadata"
            ):
                ensure_network(run)
        docker = FakeDocker([network("other", "invalid-subnet")])
        with self.assertRaisesRegex(GatewayNetworkError, "Cannot validate.*no network was changed"):
            ensure_network(docker)

    def test_command_failure_does_not_print_subprocess_diagnostics(self):
        result = SimpleNamespace(returncode=1, stdout=b"", stderr=b"synthetic private diagnostic")
        for args in [("docker", "network", "ls", "-q"), ("ip", "-j", "-4", "address", "show")]:
            with self.subTest(args=args), patch(
                "gateway_network.subprocess.run", return_value=result
            ):
                with self.assertRaises(GatewayNetworkError) as caught:
                    command(*args)
                self.assertNotIn("synthetic private diagnostic", str(caught.exception))
        with patch(
            "gateway_network.subprocess.run", side_effect=subprocess.TimeoutExpired("docker", 60)
        ):
            with self.assertRaisesRegex(
                GatewayNetworkError, "Docker and ip must be available locally"
            ):
                command("docker", "network", "ls", "-q")


if __name__ == "__main__":
    unittest.main()
