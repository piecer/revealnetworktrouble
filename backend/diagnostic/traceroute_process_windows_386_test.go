//go:build windows && 386

package diagnostic

// Compile-time ABI assertion for 32-bit Windows.
var _ [112]byte = windowsJobLimitInformationBuffer{}
