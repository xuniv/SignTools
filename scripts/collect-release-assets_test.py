import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("collect_assets", Path(__file__).with_name("collect-release-assets.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class CollectReleaseAssetsTest(unittest.TestCase):
    def fixture(self, root):
        dist = root / "dist"
        binary = dist / "SignTools_linux_amd64_v1" / "SignTools"
        binary.parent.mkdir(parents=True)
        binary.write_bytes(b"validated binary")
        name = "SignTools_3.1.0-1_linux_amd64"
        checksum = hashlib.sha256(binary.read_bytes()).hexdigest()
        (dist / "checksums.txt").write_text(f"{checksum}  {name}\n")
        (dist / "artifacts.json").write_text(json.dumps([
            {"name": "SignTools", "path": str(binary), "type": "Binary", "extra": {"ID": "SignTools"}},
            {"name": name, "path": str(binary), "type": "Binary", "extra": {"Format": "binary"}},
            {"name": "checksums.txt", "path": str(dist / "checksums.txt"), "type": "Checksum"},
        ]))
        return dist, binary, name

    def test_copies_nested_binaries_with_release_names(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            dist, binary, name = self.fixture(root)
            self.assertEqual(module.collect(dist, root / "out"), 1)
            self.assertEqual((root / "out" / name).read_bytes(), binary.read_bytes())
            self.assertTrue((root / "out" / "checksums.txt").is_file())

    def test_rejects_missing_platform(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            dist, _, _ = self.fixture(root)
            with (dist / "checksums.txt").open("a") as handle:
                handle.write("0" * 64 + "  missing-platform\n")
            with self.assertRaisesRegex(ValueError, "checksum manifest"):
                module.collect(dist, root / "out")

    def test_rejects_changed_binary(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            dist, binary, _ = self.fixture(root)
            binary.write_bytes(b"changed")
            with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                module.collect(dist, root / "out")


if __name__ == "__main__":
    unittest.main()
