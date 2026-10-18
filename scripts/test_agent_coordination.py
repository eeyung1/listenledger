"""Exercise competing processes against real isolated temporary git worktrees."""
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('agent_coordination.py').resolve()
spec = importlib.util.spec_from_file_location('coordination', SCRIPT)
coordination = importlib.util.module_from_spec(spec)
spec.loader.exec_module(coordination)


class CoordinationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name) / 'repo'
        self.root.mkdir()
        self.git('init', '-q')
        self.git('config', 'user.email', 'test@example.com')
        self.git('config', 'user.name', 'Test')
        log = self.root / 'multi-agent-orchestrator/agentmeeting.md'
        log.parent.mkdir()
        log.write_text('# Meeting\n\n## Standup Entries (newest first)\nKeep this entry.\n')
        self.git('add', '.')
        self.git('commit', '-qm', 'fixture')
        self.worktrees = []
        for i in range(2):
            path = Path(self.tmp.name) / f'worker-{i}'
            self.git('worktree', 'add', '-qb', f'worker-{i}', str(path))
            self.worktrees.append(path)

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.root), *args], stderr=subprocess.STDOUT, text=True)

    def command(self, *args):
        return [sys.executable, str(SCRIPT), *args, '--repo', str(self.root)]

    def run_tool(self, *args):
        return subprocess.run(self.command(*args), capture_output=True, text=True)

    def claim(self, feature='feature', agent='one', index=0, scope='frontend'):
        return ['claim', '--feature', feature, '--agent', agent, '--worktree', str(self.worktrees[index]), '--scope', scope]

    def test_concurrent_claims_have_one_winner(self):
        commands = [self.claim(agent=str(i), index=i) for i in range(2)]
        processes = [subprocess.Popen(self.command(*args), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) for args in commands]
        outputs = [p.communicate() for p in processes]
        self.assertEqual(sorted(p.returncode for p in processes), [0, 1], outputs)
        text = (self.root / 'multi-agent-orchestrator/agentmeeting.md').read_text()
        self.assertEqual(len(coordination.rows(text)), 1)
        self.assertIn('Keep this entry.', text)

    def test_overlapping_files_conflict_even_with_different_features(self):
        self.assertEqual(self.run_tool(*self.claim()).returncode, 0)
        second = self.run_tool(*self.claim(feature='different', agent='two', index=1, scope='frontend/src/audio.ts'))
        self.assertNotEqual(second.returncode, 0)
        self.assertIn('conflicts', second.stderr)

    def test_release_requires_owner_and_migrations_are_not_recycled(self):
        result = self.run_tool(*self.claim(), '--migration', '14')
        token = json.loads(result.stdout)['token']
        self.assertNotEqual(self.run_tool('release', '--agent', 'two', '--token', token).returncode, 0)
        self.assertEqual(self.run_tool('release', '--agent', 'one', '--token', token).returncode, 0)
        self.assertNotEqual(self.run_tool(*self.claim(agent='two', index=1), '--migration', '14').returncode, 0)
        self.assertEqual(self.run_tool(*self.claim(agent='two', index=1)).returncode, 0)

    def test_different_claims_survive_concurrent_writes(self):
        commands = [self.claim(feature=f'feature-{i}', agent=str(i), index=i, scope=f'owned-{i}') for i in range(2)]
        processes = [subprocess.Popen(self.command(*args), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) for args in commands]
        outputs = [p.communicate() for p in processes]
        self.assertEqual([p.returncode for p in processes], [0, 0], outputs)
        text = (self.root / 'multi-agent-orchestrator/agentmeeting.md').read_text()
        self.assertEqual(len(coordination.rows(text)), 2)

    def test_numeric_feature_alias_honors_existing_claim(self):
        meeting = self.root / 'multi-agent-orchestrator/agentmeeting.md'
        meeting.write_text(meeting.read_text() + '| 5 | P4.4: Language | In Progress | one | |\n')
        result = self.run_tool(*self.claim(feature='P4.4'))
        self.assertEqual(result.returncode, 0, result.stderr)
        second = self.run_tool(*self.claim(feature='5', agent='two', index=1, scope='other'))
        self.assertNotEqual(second.returncode, 0)

    def test_primary_checkout_rejected(self):
        args = self.claim()
        args[args.index('--worktree') + 1] = str(self.root)
        self.assertNotEqual(self.run_tool(*args).returncode, 0)

    def test_claim_token_scope_check(self):
        token = json.loads(self.run_tool(*self.claim()).stdout)['token']
        self.assertEqual(self.run_tool('check', '--agent', 'one', '--token', token, '--scope', 'frontend/src/audio.ts').returncode, 0)
        self.assertNotEqual(self.run_tool('check', '--agent', 'one', '--token', token, '--scope', 'backend/api/settings.go').returncode, 0)
        self.assertNotEqual(self.run_tool('check', '--agent', 'one', '--token', token, '--scope', '.').returncode, 0)

    def test_duplicate_migrations_and_dialect_mismatch_rejected(self):
        for directory in ('migrations', 'migrations_pg'):
            path = self.root / 'backend/storage' / directory
            path.mkdir(parents=True)
            (path / '0014_language.sql').write_text('-- migration')
        self.assertEqual(self.run_tool('migrations').returncode, 0)
        duplicate = self.root / 'backend/storage/migrations/0014_other.sql'
        duplicate.write_text('-- duplicate')
        self.assertIn('Duplicate migration', self.run_tool('migrations').stderr)
        duplicate.unlink()
        (self.root / 'backend/storage/migrations_pg/0014_language.sql').unlink()
        self.assertIn('differ', self.run_tool('migrations').stderr)


if __name__ == '__main__':
    unittest.main()
