package main

// releaseLifecycleFakeDocker is a stateful Docker CLI used by release tests.
// It models exact names, labels, immutable IDs, archive exports, and removals so
// the verifier's ownership checks are exercised instead of bypassed by a
// stateless success stub.
func releaseLifecycleFakeDocker() string {
	return `#!/usr/bin/env python3
import json, os, pathlib, sys

args = sys.argv[1:]
root = pathlib.Path(sys.argv[0]).resolve().parent / "docker-state"
root.mkdir(exist_ok=True)
state_path = root / "state.json"
events_path = root / "events.log"
try:
    state = json.loads(state_path.read_text())
except (FileNotFoundError, json.JSONDecodeError):
    state = {"images": {}, "containers": {}, "networks": {}}

def save():
    state_path.write_text(json.dumps(state, sort_keys=True))

def record_event(event):
    with events_path.open("a") as stream:
        stream.write(event + "\n")

def event_seen(event):
    try:
        return event in events_path.read_text().splitlines()
    except FileNotFoundError:
        return False

def log():
    path = os.environ.get("FAKE_DOCKER_LOG")
    if path:
        with open(path, "a") as stream:
            stream.write("PWD=%s ARGS=%s\n" % (os.getcwd(), " ".join(args)))

def option(name, default=""):
    try:
        return args[args.index(name) + 1]
    except (ValueError, IndexError):
        return default

def inspect_ref():
    skip = False
    for value in args[2:]:
        if skip:
            skip = False
            continue
        if value == "--format":
            skip = True
            continue
        return value
    return ""

def labels():
    result = {}
    for index, value in enumerate(args[:-1]):
        if value == "--label" and "=" in args[index + 1]:
            key, item = args[index + 1].split("=", 1)
            result[key] = item
    return result

def missing(kind, ref):
    sys.stderr.write("Error: No such %s: %s\n" % (kind, ref))
    raise SystemExit(1)

def image_record(ref):
    record = state["images"].get(ref)
    if record:
        return record
    for candidate in state["images"].values():
        if candidate["id"] == ref:
            return candidate
    missing("image", ref)

log()
if args == ["info"]:
    if os.environ.get("FAKE_MUTATE_CHECKOUT") == "true":
        pathlib.Path(os.environ["FAKE_REPO"], "payload").write_text("mutated\n")
    raise SystemExit(0)
if args[:2] == ["image", "inspect"]:
    ref = inspect_ref()
    if ref == "alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc":
        print("sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc")
        raise SystemExit(0)
    record = image_record(ref)
    fmt = option("--format")
    if "RootFS.Layers" in fmt:
        print('["sha256:root"]')
    elif "org.opencontainers.image.version" in fmt and "|" not in fmt:
        print(record["labels"].get("org.opencontainers.image.version", "0.1.0"))
    elif "org.opencontainers.image.revision" in fmt and "|" not in fmt:
        print(record["labels"].get("org.opencontainers.image.revision", os.environ.get("FAKE_REVISION", "")))
    elif "org.opencontainers.image.revision" in fmt and "|" in fmt:
        lab = record["labels"]
        print("|".join([record["id"], record["id"], lab.get("org.opencontainers.image.version", "0.1.0"), lab.get("org.opencontainers.image.revision", os.environ.get("FAKE_REVISION", ""))]))
    elif ".Id" in fmt:
        print(record["id"])
    raise SystemExit(0)
if args[:2] == ["buildx", "build"]:
    if os.getcwd() == os.environ.get("FAKE_REPO"):
        raise SystemExit(91)
    if pathlib.Path("payload").exists() and pathlib.Path("payload").read_text().strip() != "original":
        raise SystemExit(92)
    output = next(value for value in args if value.startswith("--output=type=docker,dest="))
    destination = output.split("dest=", 1)[1].rsplit(",rewrite-timestamp=true", 1)[0]
    value = b"canonical-api-archive"
    if os.environ.get("FAKE_IMAGE_MISMATCH") == "true" and destination.endswith("api-two.tar"):
        value = b"mismatched-api-archive"
    pathlib.Path(destination).write_bytes(value)
    raise SystemExit(0)
if args[:2] in (["network", "inspect"], ["container", "inspect"]):
    kind = args[0] + "s"
    ref = inspect_ref()
    cleanup_fault = os.environ.get("FAKE_CLEANUP_INSPECT_ERROR", "")
    if cleanup_fault == "pre" and event_seen("smoke_complete"):
        sys.stderr.write("daemon unavailable during cleanup inspect\n")
        raise SystemExit(125)
    if cleanup_fault == "post" and event_seen("cleanup_removed:" + kind):
        sys.stderr.write("daemon unavailable after cleanup removal\n")
        raise SystemExit(125)
    record = state[kind].get(ref)
    if record is None:
        for candidate in state[kind].values():
            if candidate["id"] == ref:
                record = candidate
                break
    if record is None:
        missing(args[0], ref)
    fmt = option("--format")
    if fmt:
        shown_name = "/" + record["name"] if args[0] == "container" else record["name"]
        print("|".join([record["id"], shown_name, record["labels"].get("com.checknetwork.release.nonce", ""), record["labels"].get("com.checknetwork.release.owner", "")]))
    raise SystemExit(0)
if args[:2] == ["network", "create"]:
    name = args[-1]
    record = {"id": "network-" + name, "name": name, "labels": labels()}
    state["networks"][name] = record
    save(); print(record["id"]); raise SystemExit(0)
if args[:2] == ["container", "create"]:
    name = option("--name")
    image = args[-1]
    record = {"id": "container-" + name, "name": name, "image": image, "labels": labels()}
    state["containers"][name] = record
    save(); print(record["id"]); raise SystemExit(0)
if args[:2] == ["container", "start"]:
    print(args[-1]); raise SystemExit(0)
if args[:2] in (["container", "rm"], ["network", "rm"]):
    kind = args[0] + "s"; ref = args[-1]
    if os.environ.get("FAKE_CLEANUP_REMOVE_FAILURE") == args[0]:
        sys.stderr.write("daemon refused cleanup removal\n")
        raise SystemExit(2)
    for name, record in list(state[kind].items()):
        if record["id"] == ref:
            del state[kind][name]; save(); record_event("cleanup_removed:" + kind); raise SystemExit(0)
    raise SystemExit(1)
if args[:2] == ["image", "rm"]:
    ref = args[-1]
    if ref in state["images"]:
        del state["images"][ref]; save(); raise SystemExit(0)
    raise SystemExit(1)
if args and args[0] == "exec":
    command = " ".join(args[2:])
    revision = os.environ.get("FAKE_REVISION", "")
    if "traceroute -n -m 1 -w 1 127.0.0.1" in command:
        print("1  127.0.0.1  0.01 ms")
    elif "/readyz" in command:
        print('{"status":"ready"}')
    elif "/livez" in command:
        print('{"status":"live"}')
    elif "/api/v1/health" in command:
        print('{"status":"ok","version":"0.1.0","revision":"%s"}' % revision)
    raise SystemExit(0)
if args and args[0] == "logs":
    record_event("smoke_complete")
    if os.environ.get("FAKE_CLEANUP_REPLACEMENT") == "container":
        for record in state["containers"].values():
            record["id"] += "-replacement"
    save()
    print('{"msg":"server started","version":"0.1.0","revision":"%s"}' % os.environ.get("FAKE_REVISION", ""))
    raise SystemExit(0)
sys.stderr.write("unexpected fake docker invocation: %r\n" % args)
raise SystemExit(98)
`
}

func releaseLifecycleFakeValidator() string {
	return `#!/usr/bin/env python3
import hashlib, json, os, pathlib, sys
args = sys.argv[1:]
def option(name): return args[args.index(name)+1]
archive = pathlib.Path(option("--archive"))
extract = pathlib.Path(option("--extract-dir"))
second = archive.name == "api-two.tar"
binary = os.environ.get("FAKE_DOCKER_BINARY", "canonical-image-binary")
if second and os.environ.get("FAKE_BINARY_MISMATCH") == "true": binary = "mismatched-binary"
traceroute = "canonical-traceroute"
(extract / "checknetwork-api").write_text(binary)
(extract / "traceroute").write_text(traceroute)
for path in (extract / "checknetwork-api", extract / "traceroute"): path.chmod(0o755)
binary_hash = hashlib.sha256(binary.encode()).hexdigest()
expected = option("--binary-sha256") if "--binary-sha256" in args else None
if expected is not None and expected != binary_hash:
    sys.stderr.write("API archive validation failed: binary SHA-256 does not match expected value\n")
    raise SystemExit(1)
metadata = "sha256:" + ("b" if second and os.environ.get("FAKE_IMAGE_MISMATCH") == "true" else "a") * 64
result = {"schema":"checknetwork.api-archive.v1","archive":hashlib.sha256(archive.read_bytes()).hexdigest(),"config":metadata,"manifest":metadata,"rootfs":metadata,"layers":[metadata],"binary":binary_hash,"traceroute":hashlib.sha256(traceroute.encode()).hexdigest(),"go_version":"go1.22.12","go_path":"github.com/network-troubleshooting-company/checknetwork/cmd/checknetwork-api"}
print(json.dumps(result, sort_keys=True, separators=(",", ":")))
`
}
