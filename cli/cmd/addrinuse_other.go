//go:build !windows

package cmd

func isAddrInUseOS(error) bool {
	return false
}
