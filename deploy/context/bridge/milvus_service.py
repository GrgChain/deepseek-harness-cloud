"""Connection-specific Milvus REST targets with DNS pinning and TLS verification."""
import ipaddress
import socket

import httpx


def service_url(value):
    url = httpx.URL(value)
    if (url.scheme not in ("http", "https") or not url.host or url.userinfo
            or url.path not in ("", "/") or url.query or url.fragment
            or len(value) > 1000):
        raise ValueError("Invalid Milvus service address")
    return url


class MilvusTransport(httpx.BaseTransport):
    def __init__(self):
        self.transport = httpx.HTTPTransport(trust_env=False)

    def handle_request(self, request):
        original = request.url
        addresses = socket.getaddrinfo(original.host, original.port or (443 if original.scheme == "https" else 80), type=socket.SOCK_STREAM)
        if not addresses:
            raise ValueError("Milvus address unavailable")
        ips = []
        for _, _, _, _, address in addresses:
            ip = ipaddress.ip_address(address[0])
            ip = getattr(ip, "ipv4_mapped", None) or ip
            if ip.is_loopback or ip.is_link_local or ip.is_unspecified or ip.is_multicast or ip.is_reserved or ip in ipaddress.ip_network("100.64.0.0/10"):
                raise ValueError("Milvus address denied")
            if original.scheme == "http" and not ip.is_private:
                raise ValueError("Public Milvus service requires HTTPS")
            ips.append(str(ip))
        # Send to the checked IP, preserving the virtual host and TLS identity.
        request.url = original.copy_with(host=ips[0])
        request.headers["Host"] = original.netloc.decode("ascii")
        request.extensions["sni_hostname"] = original.host
        try:
            return self.transport.handle_request(request)
        finally:
            request.url = original

    def close(self):
        self.transport.close()
