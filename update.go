package plasmactlupdate

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/launchrctl/keyring"
	"github.com/launchrctl/launchr"
)

const (
	sudoCmd  = "sudo"
	doasCmd  = "doas"
	chmodCmd = "chmod"
)

var errNoWritePermission = errors.New("no write permission to binary directory")

type updateAction struct {
	k        keyring.Keyring
	c        keyring.CredentialsItem
	ext      string
	fName    string
	fTmpPath string
	fPath    string
	fDir     string
	sudoCmd  string
}

func isCommandAvailable(name string) (bool, string) {
	var cmdPath string
	cmdPath, err := exec.LookPath(name)
	if err != nil {
		return false, ""
	}
	return true, cmdPath
}

// createUpdateAction instance.
func createUpdateAction(kr keyring.Keyring, cr keyring.CredentialsItem) (*updateAction, error) {
	sudoAvailable, _ := isCommandAvailable(sudoCmd)
	doasAvailable, _ := isCommandAvailable(doasCmd)
	chmodAvailable, _ := isCommandAvailable(chmodCmd)

	if !sudoAvailable && !doasAvailable {
		return nil, fmt.Errorf("neither sudo or doas is available on your system. Please install one of them")
	}

	if !chmodAvailable {
		return nil, fmt.Errorf("chmod is not available on your system. Please install it")
	}

	var cmd string
	if sudoAvailable {
		cmd = sudoCmd
	} else {
		cmd = doasCmd
	}

	return &updateAction{k: kr, c: cr, sudoCmd: cmd}, nil
}

// Errors.
var (
	errUnsupportedOS    = errors.New("unsupported operating system")
	errUnsupportedArch  = errors.New("unsupported architecture")
	errInvalidCreds     = errors.New("failed to validate credentials")
	errMalformedKeyring = errors.New("the keyring is malformed or wrong passphrase provided")
)

// Define the URL pattern for the file.
const (
	baseURL     = "https://repositories.skilld.cloud/repository/pla-plasmactl-raw"
	releasePath = "stable_release"
	binPathMask = "%s/%s/plasmactl_%s_%s%s"
)

// initVars initialize plugin variables.
func (u *updateAction) initVars() (string, string, error) {
	// Get username and password.
	if err := u.getCredentials(); err != nil {
		return "", "", err
	}

	// Get the operating system type.
	currOS, err := u.getOS()
	if err != nil {
		return "", "", err
	}

	err = u.findExecPaths()
	if err != nil {
		return "", "", err
	}

	u.fTmpPath = filepath.Join(os.TempDir(), u.fName)

	// Get the machine architecture.
	arch, err := getArch()
	if err != nil {
		return "", "", err
	}

	u.c.URL = fmt.Sprintf("%s/%s", baseURL, releasePath)

	launchr.Log().Debug("initialized environment", "os", currOS, "arch", arch, "temp_path", u.fTmpPath, "url", u.c.URL)

	return currOS, arch, nil
}

func (u *updateAction) findExecPaths() error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}

	fi, err := os.Lstat(execPath)
	if err != nil {
		return err
	}

	var path string
	if fi.Mode()&os.ModeSymlink != 0 {
		evalPath, err := filepath.EvalSymlinks(execPath)
		if err != nil {
			return err
		}
		path = evalPath
	} else {
		path = execPath
	}

	u.fDir = filepath.Dir(path)
	u.fPath = strings.TrimSpace(path)
	u.fName = filepath.Base(path)

	return nil
}

// getCredentials stores username and password credentials.
func (u *updateAction) getCredentials() error {
	repoURL := fmt.Sprintf("%s/%s", baseURL, releasePath)
	launchr.Log().Debug("get credentials for source url of release", "url", repoURL)

	// Get credentials and save in keyring.
	ci, err := u.k.GetForURL(repoURL)
	if err != nil {
		if errors.Is(err, keyring.ErrEmptyPass) {
			return err
		} else if !errors.Is(err, keyring.ErrNotFound) {
			return errMalformedKeyring
		}

		ci.URL = repoURL
		ci.Username = u.c.Username
		ci.Password = u.c.Password
		if ci.Username == "" || ci.Password == "" {
			launchr.Term().Info().Printfln("Enter credentials for %s", ci.URL)
			if err = keyring.RequestCredentialsFromTty(&ci); err != nil {
				return err
			}
		}

		if err = u.k.AddItem(ci); err != nil {
			return err
		}

		err = u.k.Save()
	}

	u.c = ci
	return err
}

// getOS check operating system supports and set extension package file.
func (u *updateAction) getOS() (os string, err error) {
	os = runtime.GOOS

	if os != "linux" && os != "darwin" {
		launchr.Term().Error().Printfln("Unsupported operating system: %s", os)
		return "", errUnsupportedOS
	}
	return os, nil
}

// getArch get OS arch.
func getArch() (arch string, err error) {
	arch = runtime.GOARCH

	if arch == "amd64" || arch == "386" || arch == "arm64" {
		return arch, nil
	}

	launchr.Term().Printfln("Unsupported architecture: %s", arch)
	return "", errUnsupportedArch
}

// validateCredentials make request to validate credentials and return HTTP status code.
func (u *updateAction) validateCredentials() error {
	r, err := u.sendRequest()
	if err != nil {
		return err
	}
	defer r.Body.Close()

	switch r.StatusCode {
	case 0:
		launchr.Term().Error().Println("Failed to validate credentials. Access denied.")
		return errInvalidCreds
	case 200:
		launchr.Term().Success().Println("Valid credentials. Access granted.")
	case 401:
		launchr.Term().Error().Printfln("HTTP %d: Unauthorized. Credentials seems to be invalid.", r.StatusCode)
		return errInvalidCreds
	case 404:
		launchr.Term().Error().Printfln("HTTP %d: Not Found. File %s does not exist.", r.StatusCode, u.c.URL)
		return errInvalidCreds
	default:
		launchr.Term().Error().Printfln(
			"HTTP %d. An issue appeared while trying to validate credentials against %s.",
			r.StatusCode,
			u.c.URL,
		)
	}

	return nil
}

// sendRequest send HTTP request, make authorization and return response.
func (u *updateAction) sendRequest() (*http.Response, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", u.c.URL, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(u.c.Username, u.c.Password)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// getStableRelease send request and get stable release version.
func (u *updateAction) getStableRelease() (string, error) {
	resp, err := u.sendRequest()

	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	r := strings.TrimSpace(string(body))
	launchr.Term().Printfln("Stable release: %s", r)

	return r, nil

}

// downloadFile Download the file using with Basic Auth header.
func (u *updateAction) downloadFile() error {
	resp, err := u.sendRequest()
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(u.fTmpPath)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, resp.Body); err != nil {
		return err
	}

	fileInfo, err := os.Stat(u.fTmpPath)
	if err != nil {
		return err
	}

	fileMode := fileInfo.Mode()
	fileMode |= 0111
	if err = os.Chmod(u.fTmpPath, fileMode); err != nil {
		return err
	}

	return nil
}

// installFile copy file to the bin folder and remove temp file.
func (u *updateAction) installFile(dirPath string) error {
	launchr.Term().Printfln("Installing %s binary under %s", u.fName, dirPath)

	err := hasWritePermissions(dirPath)
	sudoRequired := false
	if err != nil {
		if !errors.Is(err, errNoWritePermission) {
			return err
		}

		sudoRequired = true
	}

	// Copy temp file in the plasmactl folder.
	src, err := os.Open(u.fTmpPath)
	if err != nil {
		return err
	}
	defer src.Close()

	if sudoRequired {
		info, err := os.Stat(u.fDir)
		if err != nil {
			return err
		}

		pathPerm := fmt.Sprintf("%04o", info.Mode().Perm())
		// Set temp permissions for the folder with plasmactl.
		if err = u.setPermissions("777", u.fDir, sudoRequired); err != nil {
			return err
		}

		defer func() {
			if errDefer := u.setPermissions(pathPerm, u.fDir, sudoRequired); errDefer != nil {
				launchr.Log().Error("error during setting folder permissions", "dir", u.fDir, "error", errDefer)
			}
		}()
	}

	fTmpName := u.fPath + ".tmp"
	dst, err := os.Create(filepath.Clean(fTmpName))
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	if err != nil {
		return err
	}

	// Rename temp file to plasmactl.
	if err = os.Rename(fTmpName, u.fPath); err != nil {
		return err
	}

	// Set plasmactl permissions.
	if err = u.setPermissions("755", u.fPath, sudoRequired); err != nil {
		return err
	}

	return nil
}

func (u *updateAction) setPermissions(permissions, target string, sudo bool) error {
	var cmd *exec.Cmd

	if sudo {
		if u.sudoCmd == "sudo" {
			cmd = exec.Command(sudoCmd, chmodCmd, permissions, target)
		} else {
			cmd = exec.Command(doasCmd, chmodCmd, permissions, target)
		}
	} else {
		cmd = exec.Command(chmodCmd, permissions, target)
	}

	err := cmd.Run()
	return err
}

// exitWithError exit with error and remove temp file.
func (u *updateAction) exitWithError() {
	if _, err := os.Stat(u.fTmpPath); err == nil {
		if err = os.Remove(u.fTmpPath); err != nil {
			launchr.Log().Error("error deleting file", "file", u.fTmpPath, "error", err)
		}
	}

	launchr.Term().Error().Println("Update failed")
}

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
