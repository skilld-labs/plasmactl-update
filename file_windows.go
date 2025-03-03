//go:build windows

package plasmactlupdate

func hasWritePermissions(path string) error {
	//@todo handle windows update

	return errNoWritePermission
}
