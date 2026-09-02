//go:build windows && (amd64 || arm64)

package diagnostic

// Compile-time ABI assertion for supported 64-bit Windows architectures.
var _ [144]byte = windowsJobLimitInformationBuffer{}
