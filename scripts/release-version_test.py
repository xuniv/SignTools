import importlib.util
from pathlib import Path
import sys
import unittest

sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location("release_version", Path(__file__).with_name("release-version.py"))
release_version = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release_version)


class ReleaseVersionTests(unittest.TestCase):
    def test_first_patch_ignores_upstream_and_other_versions(self):
        self.assertEqual(release_version.next_version("3.1.0\n", ["v3.1.0", "v3.0.13-9"]), "3.1.0-1")

    def test_increments_highest_integer_not_lexical_order(self):
        self.assertEqual(release_version.next_version("v3.1.0", ["v3.1.0-9", "v3.1.0-10", "3.1.0-3"]), "3.1.0-11")

    def test_ignores_non_release_suffixes(self):
        self.assertEqual(release_version.next_version("3.1.0", ["v3.1.0-0", "v3.1.0-01", "v3.1.0-2-dev", "v3.1.0-4"]), "3.1.0-5")

    def test_rejects_invalid_upstream(self):
        for version in ("3.1", "3.1.0-1", "3.1.0\nother", "3.1.0;echo secret"):
            with self.subTest(version=version), self.assertRaises(ValueError):
                release_version.next_version(version, [])


if __name__ == "__main__":
    unittest.main()
