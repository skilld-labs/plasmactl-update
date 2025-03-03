//go:build unix

package plasmactlupdate

import (
	"fmt"
	"os"
	"os/user"
	"syscall"
)

func hasWritePermissions(path string) error {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("error checking binary directory permissions: %w", err)
	}

	fileStat := fileInfo.Sys().(*syscall.Stat_t)
	currentUID := os.Getuid()
	fileOwnerUID := int(fileStat.Uid)
	fileGroupGID := int(fileStat.Gid)

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("error getting current user: %w", err)
	}
	groups, err := currentUser.GroupIds()
	if err != nil {
		return fmt.Errorf("error getting user groups: %w", err)
	}

	userGroups := make(map[string]bool)
	for _, g := range groups {
		userGroups[g] = true
	}

	hasOwnerWrite := fileInfo.Mode().Perm()&(1<<(uint(7))) != 0
	hasGroupWrite := fileInfo.Mode().Perm()&(1<<(uint(4))) != 0 && userGroups[fmt.Sprint(fileGroupGID)]

	if (currentUID == fileOwnerUID && hasOwnerWrite) || hasGroupWrite {
		return nil
	}

	return errNoWritePermission
}
