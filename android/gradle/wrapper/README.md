# Gradle wrapper provenance

The checked-in wrapper selects Gradle **9.9.0** and the binary distribution. Its JAR and distribution checksum were independently compared with the official version-specific checksum endpoints. To regenerate the same pinned wrapper:

```sh
# Regenerate the complete wrapper with the independently verified distribution pin.
gradle wrapper --gradle-version 9.9.0 --distribution-type bin \
  --gradle-distribution-sha256-sum 0b00b0ffe07c4d4789c12d4935a44a7057d55774b7540bd4a3f76f336deb177a
```

The wrapper JAR is intentionally committed. Do not add it to `.gitignore`, replace it from an untrusted build, or update only part of the wrapper. Regenerate all wrapper files together and review their diff when upgrading Gradle.

## Pinned checksums

| Artifact | SHA-256 |
| --- | --- |
| `gradle-wrapper.jar` | `621008afc4f5d59357fcf06ffa644dd5aade5e091bdc363742710445f73cdaf8` |
| `gradle-9.9.0-bin.zip` | `0b00b0ffe07c4d4789c12d4935a44a7057d55774b7540bd4a3f76f336deb177a` |

The distribution checksum is published at:
<https://services.gradle.org/distributions/gradle-9.9.0-bin.zip.sha256>

`gradle-wrapper.properties` pins that value with `distributionSha256Sum`, so Gradle verifies a downloaded distribution before use. The repository-level verifier also pins the checked-in JAR, the distribution checksum, and the expected HTTPS distribution URL:

```sh
# Verify JAR, distribution checksum and expected URL before executing Gradle.
make android-wrapper-verify
```

To avoid Java Properties/awk interpretation differences, the verifier uses `od` to check every raw byte before line parsing or comment skipping, rejecting NUL and other control bytes. It accepts only printable ASCII with LF line endings: unique, unescaped keys matching `[A-Za-z][A-Za-z0-9_.-]*`, exactly one `=` per property line, and single-line values. Leading key whitespace, continuation lines (trailing `\`), raw control characters (including CR), and duplicate keys are rejected. Ordinary `#`/`!` comments and blank lines are allowed; the checked-in escaped HTTPS URL remains required.

When upgrading, obtain the new distribution checksum from Gradle's official endpoint, regenerate the wrapper, review the scripts/JAR/properties changes, and update this document and `scripts/verify-android-wrapper.sh` in the same change.
