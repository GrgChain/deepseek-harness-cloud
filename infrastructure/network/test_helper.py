import importlib.util
import json
from pathlib import Path
import unittest
import uuid

spec = importlib.util.spec_from_file_location('network_helper', Path(__file__).with_name('helper.py'))
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)

class NetworkContractTest(unittest.TestCase):
    def test_strict_contract(self):
        value = {'schema_version': 1, 'instance_id': str(uuid.uuid4())}
        self.assertEqual(helper.parse(json.dumps(value)), value)
        for patch in [{'schema_version': True}, {'instance_id': '../host'}, {'bridge': 'docker0'}]:
            with self.assertRaises(ValueError):
                helper.parse(json.dumps({**value, **patch}))
        with self.assertRaises(ValueError):
            helper.parse(json.dumps(value)[:-1] + ',"schema_version":1}')

    def test_external_model_fence_preserves_private_isolation_and_public_web(self):
        value = {'schema_version': 2, 'instance_id': str(uuid.uuid4()), 'gateway': {'addresses': ['10.50.0.10/32'], 'port': 14000}}
        self.assertEqual(helper.parse(json.dumps(value)), value)
        entries = helper.rules(value)[1][-1]
        approved = ['-d', '10.50.0.10/32', '-p', 'tcp', '-m', 'tcp', '--dport', '14000', '-j', 'ACCEPT']
        web = ['-p', 'tcp', '-m', 'multiport', '--dports', '80,443', '-j', 'RETURN']
        self.assertIn(approved, entries)
        self.assertIn(web, entries)
        dns = [['-p', protocol, '-m', protocol, '--dport', '53', '-j', 'RETURN'] for protocol in ['udp', 'tcp']]
        for rule in dns:
            self.assertIn(rule, entries)
        for network in helper.PRIVATE:
            denied = ['-d', network, '-j', 'DROP']
            self.assertLess(entries.index(approved), entries.index(denied))
            self.assertLess(entries.index(denied), entries.index(web))
            for rule in dns:
                self.assertLess(entries.index(denied), entries.index(rule))
        self.assertEqual(entries[-1], ['-j', 'DROP'])
        self.assertEqual(helper.rules(value)[0][-1], [entries[0], approved, ['-j', 'DROP']])
        for address in ['127.0.0.1/32', '169.254.169.254/32', '10.0.0.0/8', '0.0.0.0/0']:
            with self.assertRaises(ValueError):
                helper.parse(json.dumps({**value, 'gateway': {'addresses': [address], 'port': 14000}}))

    def test_parent_cannot_bypass_or_duplicate_fence(self):
        parent, bridge, chain = 'DOCKER-USER', 'hci-123456789ab', 'HC-F-123456789ab'
        jump = f'-A {parent} -i {bridge} -j {chain}'
        other = f'-A {parent} -i hci-abcdef01234 -j HC-F-abcdef01234'
        helper.verify_parent(['-N DOCKER-USER', jump, '-A DOCKER-USER -j RETURN'], parent, bridge, chain)
        helper.verify_parent(['-N DOCKER-USER', other, jump], parent, bridge, chain)
        for prior in ['-A DOCKER-USER -j ACCEPT', '-A DOCKER-USER -j RETURN', '-A DOCKER-USER -i hci-123456789ab -j ACCEPT', '-A DOCKER-USER -i hci-abcdef01234 -j ACCEPT']:
            with self.assertRaises(ValueError):
                helper.verify_parent(['-N DOCKER-USER', prior, jump], parent, bridge, chain)
        with self.assertRaises(ValueError):
            helper.verify_parent(['-N DOCKER-USER', jump, jump], parent, bridge, chain)

if __name__ == '__main__':
    unittest.main()
