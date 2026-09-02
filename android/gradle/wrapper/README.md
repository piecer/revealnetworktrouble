# Gradle wrapper provenance

The checked-in wrapper was generated from Gradle **8.11.1** with the standard wrapper task and the binary distribution:

```sh
gradle wrapper --gradle-version 8.11.1 --distribution-type bin
```

The wrapper JAR is intentionally committed. Do not add it to `.gitignore`, replace it from an untrusted build, or update only part of the wrapper. Regenerate all wrapper files together and review their diff when upgrading Gradle.

## Pinned checksums

| Artifact | SHA-256 |
| --- | --- |
| `gradle-wrapper.jar` | `2db75c40782f5e8ba1fc278a5574bab070adccb2d21ca5a6e5ed840888448046` |
| `gradle-8.11.1-bin.zip` | `f397b287023acdba1e9f6fc5ea72d22dd63669d59ed4a289a29b1a76eee151c6` |

The distribution checksum is published at:
<https://services.gradle.org/distributions/gradle-8.11.1-bin.zip.sha256>

`gradle-wrapper.properties` pins that value with `distributionSha256Sum`, so Gradle verifies a downloaded distribution before use. The repository-level verifier also pins the checked-in JAR, the distribution checksum, and the expected HTTPS distribution URL:

```sh
make android-wrapper-verify
```

When upgrading, obtain the new distribution checksum from Gradle's official endpoint, regenerate the wrapper, review the scripts/JAR/properties changes, and update this document and `scripts/verify-android-wrapper.sh` in the same change.
