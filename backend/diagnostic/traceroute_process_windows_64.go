//go:build windows && !386

package diagnostic

// windowsJobLimitInformationBuffer is the Win32
// JOBOBJECT_EXTENDED_LIMIT_INFORMATION ABI payload on 64-bit Windows.
type windowsJobLimitInformationBuffer [144]byte
