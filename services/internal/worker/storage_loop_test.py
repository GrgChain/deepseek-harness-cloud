import importlib.util
import json
import os
from pathlib import Path
import stat
import types
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('storage_loop',Path(__file__).with_name('storage_loop.py'))
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class LoopAttachmentTest(unittest.TestCase):
    def test_existing_original_is_reused_without_new_kernel_allocation(self):
        with patch.object(module,'loop_names',return_value=['/dev/loop73']),patch.object(module,'loop_nodes') as nodes,patch.object(module.fcntl,'ioctl') as allocate:
            self.assertEqual(module.attach_owned_loop('/assets/fixture/disk.img'),'/dev/loop73')
            nodes.assert_called_once_with(73)
            allocate.assert_not_called()

    def test_new_kernel_minor_gets_nodes_and_uncertain_write_is_adopted(self):
        with patch.object(module,'loop_names',side_effect=[[],['/dev/loop74']]),patch.object(module,'loop_nodes') as nodes,patch.object(module.os,'open',return_value=7),patch.object(module.os,'close'),patch.object(module.fcntl,'ioctl',return_value=74),patch.object(module.subprocess,'run',return_value=types.SimpleNamespace(returncode=1)) as attach:
            self.assertEqual(module.attach_owned_loop('/assets/fixture/restore.img'),'/dev/loop74')
            nodes.assert_called_once_with(74)
            self.assertEqual(attach.call_count,1)

    def test_busy_minor_is_retried_only_after_no_attachment_is_observed(self):
        with patch.object(module,'loop_names',side_effect=[[],[],['/dev/loop75']]),patch.object(module,'loop_nodes'),patch.object(module.os,'open',return_value=7),patch.object(module.os,'close'),patch.object(module.fcntl,'ioctl',side_effect=[74,75]),patch.object(module.subprocess,'run',return_value=types.SimpleNamespace(returncode=0)) as attach:
            self.assertEqual(module.attach_owned_loop('/assets/fixture/disk.img'),'/dev/loop75')
            self.assertEqual(attach.call_count,2)

    def test_foreign_or_ambiguous_device_is_rejected(self):
        output=json.dumps({'loopdevices':[{'name':'/dev/loop73'},{'name':'/dev/loop74'}]})
        with patch.object(module.subprocess,'run',return_value=types.SimpleNamespace(stdout=output)):
            with self.assertRaises(ValueError): module.loop_names('/assets/fixture/disk.img')
        info=types.SimpleNamespace(st_mode=stat.S_IFBLK,st_rdev=os.makedev(7,99))
        with patch.object(module.os,'mknod'),patch.object(Path,'stat',return_value=info):
            with self.assertRaises(ValueError): module.loop_nodes(74)

    def test_unbound_path_is_rejected_before_kernel_access(self):
        with patch.object(module.os,'open') as opened:
            with self.assertRaises(ValueError): module.attach_owned_loop('/foreign/disk.img')
            opened.assert_not_called()

if __name__=='__main__': unittest.main()
