"""Initialize the native Go model gateway without a model-proxy Docker image."""
import ipaddress
import json
import os
from pathlib import Path
import secrets
import stat
import subprocess
from urllib.parse import urlparse, urlunparse

ROOT = Path(__file__).resolve().parents[2]
PRIVATE = ROOT / '.local/model-gateway'
NETWORK, SUBNET = 'hc-model-gateway', '10.51.0.0/24'


def run(*args, input=None):
    result = subprocess.run(args, input=input, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=60)
    if result.returncode:
        raise RuntimeError('Native model gateway initialization failed')
    return result.stdout


def private_file(path, content=None):
    if content is not None and not path.exists():
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_uid != os.getuid():
        raise RuntimeError('Private regular file required')
    return path.read_bytes()


def main():
    PRIVATE.mkdir(mode=0o700, exist_ok=True)
    info = PRIVATE.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_mode & 0o077 or info.st_uid != os.getuid():
        raise RuntimeError('Private directory required')
    names = run('docker', 'network', 'ls', '-q').decode().split()
    networks = json.loads(run('docker', 'network', 'inspect', *names)) if names else []
    existing = next((n for n in networks if n['Name'] == NETWORK), None)
    if existing:
        if existing['Driver'] != 'bridge' or existing.get('Labels', {}).get('hc.model.gateway') != 'local' or existing['IPAM']['Config'][0]['Subnet'] != SUBNET:
            raise RuntimeError('Existing network does not match the controlled gateway')
    else:
        for n in networks:
            for row in n.get('IPAM', {}).get('Config') or []:
                if row.get('Subnet') and ipaddress.ip_network(SUBNET).overlaps(ipaddress.ip_network(row['Subnet'])):
                    raise RuntimeError('Gateway network overlaps another network')
        run('docker', 'network', 'create', '--subnet', SUBNET, '--label', 'hc.model.gateway=local', NETWORK)
    dsn = urlparse(private_file(ROOT / '.local/migrator-dsn').decode().strip())
    def sql(query):
        return run('docker', 'exec', '-i', 'hc-local-postgres-1', 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-At', '-U', dsn.username, '-d', dsn.path.lstrip('/'), input=query.encode()).decode().strip()
    role_path = ROOT / '.local/model_gateway-dsn'
    if not role_path.exists():
        if sql("SELECT count(*) FROM pg_roles WHERE rolname='hc_model_gateway'") != '0':
            raise RuntimeError('Native gateway database role requires its original private credentials')
        password = secrets.token_hex(32)
        sql("CREATE ROLE hc_model_gateway LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '" + password + "'")
        private_file(role_path, (urlunparse(dsn._replace(netloc='hc_model_gateway:' + password + '@' + dsn.hostname + ':' + str(dsn.port))) + '\n').encode())
    private_file(role_path)
    private_file(PRIVATE / 'control-key', ('sk-' + secrets.token_hex(32)).encode())
    old_umask = os.umask(0o077)
    try:
        if not (PRIVATE / 'ca.pem').exists():
            run('openssl', 'req', '-x509', '-newkey', 'rsa:3072', '-nodes', '-days', '365', '-subj', '/CN=Harness Cloud Model CA', '-keyout', str(PRIVATE / 'ca-key.pem'), '-out', str(PRIVATE / 'ca.pem'))
        if not (PRIVATE / 'ingress-cert.pem').exists():
            run('openssl', 'req', '-newkey', 'rsa:3072', '-nodes', '-subj', '/CN=10.51.0.1', '-keyout', str(PRIVATE / 'ingress-key.pem'), '-out', str(PRIVATE / 'ingress.csr'))
            private_file(PRIVATE / 'extensions', b'subjectAltName=IP:10.51.0.1\nextendedKeyUsage=serverAuth\n')
            run('openssl', 'x509', '-req', '-in', str(PRIVATE / 'ingress.csr'), '-CA', str(PRIVATE / 'ca.pem'), '-CAkey', str(PRIVATE / 'ca-key.pem'), '-CAcreateserial', '-days', '365', '-extfile', str(PRIVATE / 'extensions'), '-out', str(PRIVATE / 'ingress-cert.pem'))
    finally:
        os.umask(old_umask)
    for name in ['ingress-cert.pem', 'ingress-key.pem', 'ca.pem']:
        private_file(PRIVATE / name)
    options = {'schema_version': 1, 'address': '10.51.0.1:14000', 'certificate_file': str(PRIVATE / 'ingress-cert.pem'), 'certificate_key_file': str(PRIVATE / 'ingress-key.pem'), 'control_key_file': str(PRIVATE / 'control-key'), 'data_key_file': str(ROOT / '.local/auth/data-key'), 'data_key_id': 'local-v1', 'environment': 'local'}
    private_file(PRIVATE / 'config.json', json.dumps(options, indent=2).encode())
    policy = {'endpoint': 'https://10.51.0.1:14000', 'revision': 'local-gateway-v1', 'addresses': ['10.51.0.1/32'], 'ca_file': str(PRIVATE / 'ca.pem'), 'probe_retries_disabled': True, 'probe_fallbacks_disabled': True, 'models': []}
    private_file(PRIVATE / 'gateway-policy.json', json.dumps(policy, indent=2).encode())
    print('Native Go model gateway configuration ready; run migrations/grants, configure Worker, then start the Go process.')


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, OSError, ValueError, KeyError, subprocess.TimeoutExpired):
        raise SystemExit('Native gateway initialization failed; inspect private configuration without printing credentials.') from None
