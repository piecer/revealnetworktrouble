#!/usr/bin/env python3
"""Bounded structural verifier for Android JUnit XML results."""

from __future__ import annotations

import os
import stat
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, List, NoReturn, Optional
from xml.parsers import expat

MAX_XML_FILES = 4096
MAX_XML_FILE_BYTES = 64 * 1024 * 1024
MAX_XML_TOTAL_BYTES = 256 * 1024 * 1024
MAX_XML_ELEMENTS = 2_000_000
MAX_XML_DEPTH = 128
MAX_XML_TEXT_CHARS = 64 * 1024 * 1024
READ_CHUNK_BYTES = 64 * 1024

BINARY_METADATA_DIRECTORY = "binary"
BINARY_METADATA_FILES = frozenset(("output.bin", "output.bin.idx", "results.bin"))
MAX_BINARY_METADATA_ENTRIES = len(BINARY_METADATA_FILES)
MAX_BINARY_METADATA_DEPTH = 1
MAX_BINARY_METADATA_FILE_BYTES = 64 * 1024 * 1024
MAX_BINARY_METADATA_TOTAL_BYTES = 128 * 1024 * 1024

DEBUG_CLASS = "com.checknetwork.app.DebugNetworkSecurityContractTest"
RELEASE_CLASS = "com.checknetwork.app.ReleaseNetworkSecurityContractTest"


class VerificationError(Exception):
    pass


@dataclass(frozen=True)
class SuiteResult:
    tests: int
    root_name: str
    testcase_count: int
    testcase_classes: tuple[str, ...]


def fail(message: str) -> NoReturn:
    raise VerificationError(message)


def bounded_integer(attributes: Dict[str, str], key: str, source: Path) -> int:
    value = attributes.get(key)
    if value is None or not value or any(character < "0" or character > "9" for character in value):
        fail(f"malformed Android unit-test XML attribute {key}: {source}")
    parsed = int(value)
    if parsed > MAX_XML_ELEMENTS:
        fail(f"Android unit-test XML attribute {key} exceeds bound: {source}")
    return parsed


def parse_suite(source: Path, forbidden_class: str) -> SuiteResult:
    size = source.stat(follow_symlinks=False).st_size
    if size > MAX_XML_FILE_BYTES:
        fail(f"Android unit-test XML exceeds byte limit: {source}")

    parser = expat.ParserCreate()
    parser.SetParamEntityParsing(expat.XML_PARAM_ENTITY_PARSING_NEVER)
    depth = 0
    elements = 0
    text_chars = 0
    root_name: Optional[str] = None
    root_attributes: Optional[Dict[str, str]] = None
    testcase_classes: List[str] = []

    def start(name: str, attributes: Dict[str, str]) -> None:
        nonlocal depth, elements, root_name, root_attributes
        depth += 1
        elements += 1
        if depth > MAX_XML_DEPTH or elements > MAX_XML_ELEMENTS:
            fail(f"Android unit-test XML exceeds structural bound: {source}")
        if depth == 1:
            root_name = name
            root_attributes = dict(attributes)
        if name == "testsuite" and attributes.get("name") == forbidden_class:
            fail(f"Android unit-test XML contains forbidden suite {forbidden_class}: {source}")
        if name == "testcase":
            classname = attributes.get("classname")
            if classname is None:
                fail(f"Android unit-test testcase lacks classname: {source}")
            if classname == forbidden_class:
                fail(f"Android unit-test XML contains forbidden class {forbidden_class}: {source}")
            testcase_classes.append(classname)
        if name in ("error", "failure"):
            fail(f"Android unit-test XML contains {name}: {source}")

    def end(_name: str) -> None:
        nonlocal depth
        depth -= 1

    def character_data(data: str) -> None:
        nonlocal text_chars
        text_chars += len(data)
        if text_chars > MAX_XML_TEXT_CHARS:
            fail(f"Android unit-test XML text exceeds bound: {source}")

    def reject_doctype(*_args: object) -> None:
        fail(f"Android unit-test XML must not contain a doctype: {source}")

    def reject_external_entity(*_args: object) -> int:
        fail(f"Android unit-test XML must not contain an external entity: {source}")
        return 0

    parser.StartElementHandler = start
    parser.EndElementHandler = end
    parser.CharacterDataHandler = character_data
    parser.StartDoctypeDeclHandler = reject_doctype
    parser.ExternalEntityRefHandler = reject_external_entity

    try:
        with source.open("rb") as stream:
            consumed = 0
            while True:
                chunk = stream.read(READ_CHUNK_BYTES)
                if not chunk:
                    break
                consumed += len(chunk)
                if consumed > MAX_XML_FILE_BYTES:
                    fail(f"Android unit-test XML exceeds byte limit: {source}")
                parser.Parse(chunk, False)
            parser.Parse(b"", True)
    except VerificationError:
        raise
    except (OSError, expat.ExpatError) as error:
        fail(f"malformed Android unit-test XML: {source}: {error}")

    if root_name != "testsuite" or root_attributes is None:
        fail(f"Android unit-test XML root must be testsuite: {source}")
    tests = bounded_integer(root_attributes, "tests", source)
    errors = bounded_integer(root_attributes, "errors", source)
    failures = bounded_integer(root_attributes, "failures", source)
    if tests == 0:
        fail(f"Android unit-test XML reports zero tests: {source}")
    if errors != 0 or failures != 0:
        fail(f"Android unit-test XML reports failures or errors: {source}")
    return SuiteResult(tests, root_attributes.get("name", ""), len(testcase_classes), tuple(testcase_classes))


def validate_binary_companion(root: Path) -> None:
    """Validate Gradle's bounded non-evidence binary result metadata."""
    try:
        with os.scandir(root) as iterator:
            entries = sorted(iterator, key=lambda entry: entry.name)
    except OSError as error:
        fail(f"cannot read Android unit-test binary metadata directory {root}: {error}")

    if len(entries) > MAX_BINARY_METADATA_ENTRIES:
        fail(f"Android unit-test binary metadata entry count exceeds bound: {root}")

    names: set[str] = set()
    total_bytes = 0
    for entry in entries:
        path = Path(entry.path)
        try:
            entry_stat = entry.stat(follow_symlinks=False)
        except OSError as error:
            fail(f"cannot inspect Android unit-test binary metadata path {path}: {error}")
        if stat.S_ISLNK(entry_stat.st_mode):
            fail(f"Android unit-test binary metadata path must not be a symlink: {path}")
        if stat.S_ISDIR(entry_stat.st_mode):
            fail(
                "Android unit-test binary metadata exceeds "
                f"depth bound {MAX_BINARY_METADATA_DEPTH}: {path}"
            )
        if not stat.S_ISREG(entry_stat.st_mode):
            fail(f"Android unit-test binary metadata path must be a regular file: {path}")
        if entry.name.endswith(".xml"):
            fail(f"Android unit-test XML must be a direct result child, not binary metadata: {path}")
        if entry.name not in BINARY_METADATA_FILES:
            fail(f"unexpected Android unit-test binary metadata file: {path}")
        if entry_stat.st_size > MAX_BINARY_METADATA_FILE_BYTES:
            fail(f"Android unit-test binary metadata exceeds per-file byte limit: {path}")
        names.add(entry.name)
        total_bytes += entry_stat.st_size
        if total_bytes > MAX_BINARY_METADATA_TOTAL_BYTES:
            fail(f"Android unit-test binary metadata exceeds total byte limit: {root}")

    if names != BINARY_METADATA_FILES:
        fail(f"Android unit-test binary metadata layout is incomplete: {root}")


def collect_xml(root: Path) -> List[Path]:
    try:
        root_stat = root.lstat()
    except OSError:
        fail(f"missing Android unit-test result directory: {root}")
    if stat.S_ISLNK(root_stat.st_mode) or not stat.S_ISDIR(root_stat.st_mode):
        fail(f"invalid Android unit-test result directory: {root}")

    try:
        entries = sorted(os.scandir(root), key=lambda entry: entry.name)
    except OSError as error:
        fail(f"cannot read Android unit-test result directory {root}: {error}")

    files: List[Path] = []
    basenames: set[str] = set()
    for entry in entries:
        path = Path(entry.path)
        try:
            entry_stat = entry.stat(follow_symlinks=False)
        except OSError as error:
            fail(f"cannot inspect Android unit-test result path {path}: {error}")
        if stat.S_ISLNK(entry_stat.st_mode):
            fail(f"Android unit-test result path must not be a symlink: {path}")
        if stat.S_ISDIR(entry_stat.st_mode) and entry.name == BINARY_METADATA_DIRECTORY:
            validate_binary_companion(path)
            continue
        if not stat.S_ISREG(entry_stat.st_mode):
            fail(f"Android unit-test result path must be a regular file: {path}")
        if not entry.name.endswith(".xml"):
            fail(f"Android unit-test result path must be XML: {path}")
        if entry.name in basenames:
            fail(f"Android unit-test XML basenames must be unique: {entry.name}")
        basenames.add(entry.name)
        files.append(path)
        if len(files) > MAX_XML_FILES:
            fail(f"Android unit-test XML file count exceeds bound: {root}")

    if not files:
        fail(f"Android unit-test result directory has no XML: {root}")
    total_bytes = sum(path.stat(follow_symlinks=False).st_size for path in files)
    if total_bytes > MAX_XML_TOTAL_BYTES:
        fail(f"Android unit-test XML collection exceeds byte limit: {root}")
    return sorted(files, key=lambda path: os.fsencode(str(path)))


def verify_variant(root: Path, variant: str, expected_class: str, forbidden_class: str) -> None:
    files = collect_xml(root)
    expected_basename = f"TEST-{expected_class}.xml"
    expected_files = [path for path in files if path.name == expected_basename]
    if len(expected_files) != 1:
        fail(f"Android {variant} collection requires exactly one {expected_basename}: {root}")

    total = 0
    expected_tests = 0
    for source in files:
        suite = parse_suite(source, forbidden_class)
        total += suite.tests
        if source == expected_files[0]:
            if suite.root_name != expected_class:
                fail(f"Android {variant} expected suite name mismatch: {source}")
            if suite.testcase_count != suite.tests or any(name != expected_class for name in suite.testcase_classes):
                fail(f"Android {variant} expected suite class identity mismatch: {source}")
            expected_tests = suite.tests

    if total <= 0 or expected_tests <= 0:
        fail(f"Android {variant} unit-test XML reports zero collected tests: {root}")
    print(
        f"Android {variant} unit-test collection verified: {root} "
        f"({total} tests; {expected_tests} variant-contract tests)"
    )


def main(arguments: List[str]) -> int:
    if len(arguments) != 2:
        print(f"usage: {Path(sys.argv[0]).name} DEBUG-TEST-RESULT-DIR RELEASE-TEST-RESULT-DIR", file=sys.stderr)
        return 2
    try:
        verify_variant(Path(arguments[0]), "debug", DEBUG_CLASS, RELEASE_CLASS)
        verify_variant(Path(arguments[1]), "release", RELEASE_CLASS, DEBUG_CLASS)
    except VerificationError as error:
        print(error, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
