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

    def test_external_model_fence_allows_only_approved_gateway_port(self):
        value = {'schema_version': 2, 'instance_id': str(uuid.uuid4()), 'gateway': {'addresses': ['10.50.0.10/32'], 'port': 14000}}
        self.assertEqual(helper.parse(json.dumps(value)), value)
        entries = helper.rules(value)[1][-1]
        self.assertIn(['-d', '10.50.0.10/32', '-p', 'tcp', '-m', 'tcp', '--dport', '14000', '-j', 'ACCEPT'], entries)
        self.assertEqual(entries[-1], ['-j', 'DROP'])
        self.assertEqual(helper.rules(value)[0][-1], entries)
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
