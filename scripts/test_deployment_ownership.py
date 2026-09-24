#!/usr/bin/env python3
"""Ownership fault tests for the opt-in deployment harness (no Docker calls)."""
import inspect
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from test_deployment_live import OwnedContainer, interrupted


class OwnershipTests(unittest.TestCase):
    def test_real_signals_after_unmask_still_clean_exact_id(self):
        # Interrupt the first instruction AFTER unmask returns, not inside it.
        source, first_line = inspect.getsourcelines(OwnedContainer.__enter__)
        boundary = first_line + next(i for i, line in enumerate(source)
                                     if line.strip() == 'if result.returncode:')
        for sig in (signal.SIGHUP, signal.SIGINT, signal.SIGTERM):
            with self.subTest(signal=sig.name), tempfile.TemporaryDirectory() as directory:
                owner = OwnedContainer(Path(directory), 'borrowed-image', [])
                created = 'b' * 64
                calls = []
                state = {'exists': False, 'signalled': False}

                def docker(*args, **kwargs):
                    calls.append(args)
                    info = [{'Id': created, 'Name': '/' + owner.state['name'], 'Mounts': [],
                             'Config': {'Labels': {'checknetwork.deployment.owner': owner.state['owner']}}}]
                    if args[:2] == ('container', 'inspect'):
                        if state['exists']:
                            return subprocess.CompletedProcess(args, 0, json.dumps(info), '')
                        return subprocess.CompletedProcess(args, 1, '', 'No such container')
                    if args[:2] == ('image', 'inspect'):
                        return subprocess.CompletedProcess(args, 0, json.dumps([{'Id': 'base', 'Config': {}}]), '')
                    if args[0] == 'create':
                        state['exists'] = True
                        return subprocess.CompletedProcess(args, 0, created + '\n', '')
                    if args[0] == 'inspect':
                        self.assertEqual(args, ('inspect', created))
                        self.assertTrue(state['exists'])
                        return subprocess.CompletedProcess(args, 0, json.dumps(info), '')
                    if args[0] == 'rm':
                        self.assertEqual(args, ('rm', '-f', created))
                        state['exists'] = False
                        return subprocess.CompletedProcess(args, 0, created, '')
                    self.fail(f'unexpected Docker command: {args}')

                def schedule(frame, event, _arg):
                    if (event == 'line' and frame.f_code is OwnedContainer.__enter__.__code__
                            and frame.f_lineno == boundary and not state['signalled']):
                        self.assertNotIn(sig, signal.pthread_sigmask(signal.SIG_BLOCK, set()))
                        receipt = json.loads(owner.path.read_text())
                        self.assertEqual(receipt['id'], created)
                        self.assertEqual(receipt['create_exit'], 0)
                        state['signalled'] = True
                        os.kill(os.getpid(), sig)
                    return schedule

                previous_handler = signal.signal(sig, interrupted)
                previous_trace = sys.gettrace()
                try:
                    with patch('test_deployment_live.docker', docker):
                        sys.settrace(schedule)
                        try:
                            with self.assertRaises(SystemExit) as raised:
                                with owner:
                                    self.fail('signal must precede the context body')
                        finally:
                            sys.settrace(previous_trace)
                finally:
                    signal.signal(sig, previous_handler)
                self.assertTrue(state['signalled'])
                self.assertEqual(raised.exception.code, 128 + sig)
                self.assertFalse(state['exists'], 'signal leaked the successfully created container')
                self.assertEqual(calls.count(('rm', '-f', created)), 1)
                self.assertEqual(calls[-2:], [('container', 'inspect', created),
                                             ('container', 'inspect', owner.state['name'])])
                self.assertTrue(json.loads(owner.path.read_text())['absence_verified'])

    def test_signal_after_create_receipt_still_cleans_exact_id(self):
        with tempfile.TemporaryDirectory() as directory:
            owner = OwnedContainer(Path(directory), 'borrowed-image', [])
            created = 'a' * 64
            calls = []

            def docker(*args, **kwargs):
                calls.append(args)
                if args[:2] == ('container', 'inspect'):
                    return subprocess.CompletedProcess(args, 1, '', 'No such container')
                if args[:2] == ('image', 'inspect'):
                    return subprocess.CompletedProcess(args, 0, json.dumps([{'Id': 'base', 'Config': {}}]), '')
                if args[0] == 'create':
                    # Independent successful create receipt, not a name adoption.
                    return subprocess.CompletedProcess(args, 0, created + '\n', '')
                if args[0] == 'inspect':
                    persisted = json.loads(owner.path.read_text())
                    self.assertEqual(persisted['id'], created)
                    self.assertEqual(persisted['create_exit'], 0)
                    return subprocess.CompletedProcess(args, 0, json.dumps([{
                        'Id': created, 'Name': '/' + owner.state['name'], 'Mounts': [],
                        'Config': {'Labels': {'checknetwork.deployment.owner': owner.state['owner']}}
                    }]), '')
                if args[0] == 'rm':
                    self.assertEqual(args, ('rm', '-f', created))
                    return subprocess.CompletedProcess(args, 0, created, '')
                self.fail(f'unexpected Docker command: {args}')

            def mask(how, _signals):
                if how == signal.SIG_SETMASK:
                    raise SystemExit(143)
                return set()

            with patch('test_deployment_live.docker', docker), patch('signal.pthread_sigmask', mask):
                with self.assertRaises(SystemExit):
                    owner.__enter__()
            self.assertIn(('rm', '-f', created), calls)
            self.assertTrue(json.loads(owner.path.read_text())['absence_verified'])

    def test_preexisting_exact_name_never_becomes_owned(self):
        with tempfile.TemporaryDirectory() as directory:
            owner = OwnedContainer(Path(directory), 'borrowed-image', [])
            with patch('test_deployment_live.docker', return_value=subprocess.CompletedProcess([], 0, '[{}]', '')) as docker:
                with self.assertRaises(AssertionError):
                    owner.__enter__()
                docker.assert_called_once_with('container', 'inspect', owner.state['name'], check=False)
            self.assertIsNone(owner.state['id'])

    def test_cleanup_preserves_changed_owner(self):
        with tempfile.TemporaryDirectory() as directory:
            owner = OwnedContainer(Path(directory), 'borrowed-image', [])
            owner.state['id'] = 'a' * 64
            info = [{'Id': owner.state['id'], 'Name': '/' + owner.state['name'], 'Mounts': [],
                     'Config': {'Labels': {'checknetwork.deployment.owner': 'not-ours'}}}]
            with patch('test_deployment_live.docker', return_value=subprocess.CompletedProcess([], 0, json.dumps(info), '')) as docker:
                with self.assertRaises(AssertionError):
                    owner.close()
                docker.assert_called_once_with('inspect', owner.state['id'])


if __name__ == '__main__':
    unittest.main()
