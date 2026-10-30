"""Trusted network fence executor; invoked with host networking and NET_ADMIN.

Only generated per-instance bridge names/chains are accepted. Never flush a
shared Docker or host chain. The Worker installs fences before starting compute.
"""
import json
import ipaddress
import re
import shlex
import subprocess
import sys

UUID = re.compile(r"^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$")
PRIVATE = ["0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4"]
PUBLIC_WEB_PORTS = "80,443"

def parse(data):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError("Duplicate field")
            result[key] = value
        return result
    value = json.loads(data, object_pairs_hook=unique)
    if not isinstance(value, dict) or set(value) != ({"schema_version", "instance_id", "gateway"} if value.get("schema_version") == 2 else {"schema_version", "instance_id"}) or type(value["schema_version"]) is not int or value["schema_version"] not in [1, 2] or not isinstance(value["instance_id"], str) or not UUID.fullmatch(value["instance_id"]):
        raise ValueError("Invalid network contract")
    if value['schema_version'] == 2:
        gateway = value['gateway']
        if not isinstance(gateway, dict) or set(gateway) != {'addresses', 'port'} or type(gateway['port']) is not int or not 1 <= gateway['port'] <= 65535 or not isinstance(gateway['addresses'], list) or not 1 <= len(gateway['addresses']) <= 16:
            raise ValueError('Invalid gateway fence')
        for item in gateway['addresses']:
            network = ipaddress.ip_network(item, strict=True)
            if network.version != 4 or network.prefixlen < 24 or network.network_address.is_loopback or network.network_address.is_link_local or network.network_address.is_multicast or network.network_address.is_unspecified or network.network_address.is_reserved:
                raise ValueError('Invalid gateway address')
    return value

def command(binary, *args, check=True):
    return subprocess.run([binary, "-w", "5", *args], check=check, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=10)

def rules(config):
    suffix = config["instance_id"].replace("-", "")[:11]
    bridge = "hci-" + suffix
    established = ["-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"]
    inbound = [established, ["-j", "DROP"]]
    forward = [established] + [["-d", network, "-j", "DROP"] for network in PRIVATE] + [["-j", "RETURN"]]
    if config['schema_version'] == 2:
        gateway = config['gateway']
        approved = [["-d", address, "-p", "tcp", "-m", "tcp", "--dport", str(gateway['port']), "-j", "ACCEPT"] for address in gateway['addresses']]
        # Model gateway access is an explicit private exception. Web tools and
        # installers may use public HTTP(S), after all private ranges are denied.
        dns = [["-p", protocol, "-m", protocol, "--dport", "53", "-j", "RETURN"] for protocol in ["udp", "tcp"]]
        forward = [established] + approved + [["-d", network, "-j", "DROP"] for network in PRIVATE] + dns + [["-p", "tcp", "-m", "multiport", "--dports", PUBLIC_WEB_PORTS, "-j", "RETURN"], ["-j", "DROP"]]
        inbound = [established] + approved + [["-j", "DROP"]]
    return [
        ("iptables", "HC-I-" + suffix, "INPUT", bridge, inbound),
        ("iptables", "HC-F-" + suffix, "DOCKER-USER", bridge, forward),
        ("ip6tables", "HC6-I-" + suffix, "INPUT", bridge, [["-j", "DROP"]]),
        ("ip6tables", "HC6-F-" + suffix, "FORWARD", bridge, [["-j", "DROP"]]),
    ]

def verify_parent(lines, parent, bridge, chain):
    entries = [shlex.split(line) for line in lines if line.startswith("-A ")]
    jump = ["-A", parent, "-i", bridge, "-j", chain]
    if entries.count(jump) != 1:
        raise ValueError("Network fence parent conflicts")
    prefix = chain.rsplit("-", 1)[0] + "-"
    for entry in entries[:entries.index(jump)]:
        # Other managed instance jumps may precede ours, but a broad host rule
        # could accept/return before this fence ever sees the packet.
        if len(entry) != 6 or entry[:3] != ["-A", parent, "-i"] or entry[4] != "-j" or not re.fullmatch(r"hci-[a-f0-9]{11}", entry[3]) or entry[3] == bridge or entry[5] != prefix + entry[3][4:]:
            raise ValueError("Network fence can be bypassed")

def execute(action, config):
    if action not in ["install", "inspect", "remove"]:
        raise ValueError("Unsupported network action")
    for binary, chain, parent, bridge, entries in rules(config):
        jump = ["-i", bridge, "-j", chain]
        exists = command(binary, "-S", chain, check=False).returncode == 0
        if action == "remove" and not exists:
            continue
        if not exists:
            if action != "install":
                raise ValueError("Network fence missing")
            command(binary, "-N", chain)
            for entry in entries:
                command(binary, "-A", chain, *entry)
        # Fail closed on extra rules as well as missing rules. Never replace an
        # unexpected chain that could be owned by another deployment.
        listed = command(binary, "-S", chain).stdout.splitlines()
        if len(listed) != len(entries) + 1:
            raise ValueError("Network fence conflicts")
        for entry in entries:
            command(binary, "-C", chain, *entry)
        def normalize(tokens):
            return [",".join(sorted(token.split(","))) if "," in token else token for token in tokens]
        if [normalize(shlex.split(line)) for line in listed[1:]] != [normalize(["-A", chain, *entry]) for entry in entries]:
            raise ValueError("Network rule ordering conflicts")
        if action == "remove":
            command(binary, "-D", parent, *jump, check=False)
            command(binary, "-F", chain)
            command(binary, "-X", chain)
            continue
        linked = command(binary, "-C", parent, *jump, check=False).returncode == 0
        if not linked:
            if action != "install":
                raise ValueError("Network fence detached")
            command(binary, "-I", parent, "1", *jump)
        verify_parent(command(binary, "-S", parent).stdout.splitlines(), parent, bridge, chain)
    return {"schema_version": 1, "instance_id": config["instance_id"], "isolated": action != "remove"}

if __name__ == "__main__":
    try:
        if len(sys.argv) != 2:
            raise ValueError("One action required")
        data = sys.stdin.buffer.read(65537)
        if len(data) > 65536:
            raise ValueError("Request too large")
        print(json.dumps(execute(sys.argv[1], parse(data))))
    except Exception:
        print("harness-cloud: network fence unavailable; runtime must remain stopped", file=sys.stderr)
        sys.exit(1)
