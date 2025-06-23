package plasmactlupdate

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/launchrctl/keyring"
	"github.com/launchrctl/launchr"
	"github.com/launchrctl/launchr/pkg/action"
)

// Errors.
var (
	errUnsupportedOS    = errors.New("unsupported operating system")
	errMalformedKeyring = errors.New("the keyring is malformed or wrong passphrase provided")
)

// archMap maps Go architecture strings to their corresponding common architecture names.
var archMap = map[string]string{
	"amd64": "x86_64",
	"386":   "i386",
}

const (
	sudoCmd  = "sudo"
	doasCmd  = "doas"
	chmodCmd = "chmod"
)

var errNoWritePermission = errors.New("no write permission to binary directory")

type updateAction struct {
	action.WithLogger
	action.WithTerm
	k keyring.Keyring

	cfg           *config
	targetVersion string

	// runtime vars.
	credentials  keyring.CredentialsItem
	ext          string
	fName        string
	fTmpPath     string
	fPath        string
	fDir         string
	sudoCmd      string
	appName      string
	os           string
	arch         string
	requiresAuth bool
}

func (u *updateAction) doRun() error {
	version := launchr.Version()
	u.Term().Info().Printfln("Starting %s installation...", version.Name)
	u.Log().Debug("current app info", "name", version.Name, "version", version.Version, "os", version.OS, "arch", version.Arch)

	err := u.initVars()
	if err != nil {
		return err
	}

	var versionToGet string
	if u.targetVersion != "" {
		// Get a specific version.
		versionToGet = u.targetVersion
	} else {
		// Get value of Stable Release.
		versionToGet, err = u.getStableRelease()
		if err != nil {
			return err
		}
	}

	// check if the current version is up to date.
	if version.Version == versionToGet {
		u.Term().Printfln("Current version of %s is up to date.", version.Name)
		return nil
	}

	// Download file to the temp folder.
	if err = u.downloadFile(versionToGet); err != nil {
		return err
	}

	u.Log().Debug("binary path", "path", u.fPath)

	if err = u.installFile(u.fDir); err != nil {
		return err
	}

	// Outro.
	u.Term().Success().Printfln("%s has been installed successfully.", u.fName)
	return nil
}

// initVars initialize plugin variables.
func (u *updateAction) initVars() error {
	var err error

	u.appName = launchr.Version().Name

	// Get the operating system type.
	u.os, err = getOS()
	if err != nil {
		u.Term().Error().Printfln("Unsupported operating system: %s", u.os)
		return err
	}

	// Get the machine architecture.
	u.arch = getArch()

	if u.cfg == nil {
		return fmt.Errorf("update config is not set, use --config flag or build launchr with predefined config")
	}

	err = validateConfig(u.cfg)
	if err != nil {
		u.Log().Debug("config validation failed", "error", err)
		return fmt.Errorf("not enough configuration for update. Please ensure your build is with correct tags. See debug for missing info")
	}

	// Set URL for credentials item.
	u.credentials.URL = u.cfg.RepositoryURL
	authRequired, err := u.checkAuthRequired(u.credentials.URL)
	if err != nil {
		u.Log().Debug("failed to check auth requirement, proceeding without auth", "error", err)
		authRequired = false // Proceed without requiring auth
	}

	u.requiresAuth = authRequired
	u.Log().Debug("repository auth requirement", "requires_auth", u.requiresAuth)

	// Only get credentials if auth is required
	if u.requiresAuth {
		// Get username and password.
		if err = u.getCredentials(); err != nil {
			return err
		}
	}

	// Prepare binary paths
	err = u.findExecPaths()
	if err != nil {
		return err
	}

	u.Log().Debug("initialized environment",
		"os", u.os, "arch", u.arch, "temp_path", u.fTmpPath, "url", u.credentials.URL,
		"base URL", u.cfg.RepositoryURL, "stable release", u.cfg.PinnedRelease, "bin_mask", u.cfg.BinMask,
	)
	return nil
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
	u.fTmpPath = filepath.Join(os.TempDir(), u.fName)

	return nil
}

// getCredentials stores username and password credentials.
func (u *updateAction) getCredentials() error {
	u.Log().Debug("get credentials for source url of release", "url", u.credentials.URL)

	// Get credentials and save in a keyring.
	ci, err := u.k.GetForURL(u.credentials.URL)
	if err != nil {
		if errors.Is(err, keyring.ErrEmptyPass) {
			return err
		} else if !errors.Is(err, keyring.ErrNotFound) {
			return errMalformedKeyring
		}

		ci.URL = u.credentials.URL
		ci.Username = u.credentials.Username
		ci.Password = u.credentials.Password
		if ci.Username == "" || ci.Password == "" {
			u.Term().Info().Printfln("Enter credentials for %s", ci.URL)
			if err = keyring.RequestCredentialsFromTty(&ci); err != nil {
				return err
			}
		}

		if err = u.k.AddItem(ci); err != nil {
			return err
		}

		if u.k.Exists() {
			err = u.k.Save()
			u.k.ResetStorage()
		}
	}

	u.credentials = ci
	return err
}

// checkAuthRequired determines if the repository requires authentication
func (u *updateAction) checkAuthRequired(url string) (bool, error) {
	client := &http.Client{}

	// Test with a simple HEAD request to the base repository URL
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return false, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	u.Log().Debug("auth check response", "url", url, "status_code", resp.StatusCode)

	// If we get 401 or 403, authentication is likely required
	return resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden, nil
}

// sendRequest send HTTP request, make authorization and return response.
func (u *updateAction) sendRequest(url string) (*http.Response, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Only set auth if required and we have credentials
	if u.requiresAuth && u.credentials.Username != "" && u.credentials.Password != "" {
		req.SetBasicAuth(u.credentials.Username, u.credentials.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	u.Log().Debug("request response", "url", url, "status", resp.Status, "status_code", resp.StatusCode, "method", req.Method)
	if err = u.checkResponseStatus(resp); err != nil {
		return nil, err
	}

	return resp, nil
}

func (u *updateAction) checkResponseStatus(r *http.Response) error {
	if r.StatusCode == http.StatusOK {
		return nil
	}
	var err error
	switch r.StatusCode {
	case http.StatusUnauthorized:
		err = fmt.Errorf("HTTP %d: Unauthorized. Credentials seems to be invalid", r.StatusCode)
	case http.StatusNotFound:
		err = fmt.Errorf("HTTP %d: Not Found. File %s does not exist", r.StatusCode, r.Request.URL.Path)
	default:
		err = fmt.Errorf("an issue appeared while trying to make request to %s", r.Request.URL.Path)
	}

	return err
}

// getStableRelease send request and get a stable release version.
func (u *updateAction) getStableRelease() (string, error) {
	vars := templateVars{
		URL:  u.credentials.URL,
		Name: u.appName,
		OS:   u.os,
		Arch: u.arch,
		Ext:  u.ext,
	}

	releaseURL, err := formatURL(u.cfg.PinnedRelease, vars)
	if err != nil {
		return "", fmt.Errorf("failed to format release URL: %w", err)
	}

	resp, err := u.sendRequest(releaseURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	r := strings.TrimSpace(string(body))
	u.Term().Printfln("Stable release: %s", r)

	return r, nil

}

// downloadFile Download the file using with Basic Auth header.
func (u *updateAction) downloadFile(version string) error {
	// Format the URL with the determined 'os', 'arch' and 'extension' values.
	vars := templateVars{
		URL:     u.credentials.URL,
		Name:    u.appName,
		Version: version,
		OS:      u.os,
		Arch:    u.arch,
		Ext:     u.ext,
	}

	fileURL, err := formatURL(u.cfg.BinMask, vars)
	if err != nil {
		return fmt.Errorf("failed to format download URL: %w", err)
	}

	u.Term().Printfln("Downloading file: %s", fileURL)
	resp, err := u.sendRequest(fileURL)
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
	u.Term().Printfln("Installing %s binary under %s", u.fName, dirPath)

	err := hasWritePermissions(dirPath)
	sudoRequired := false
	if err != nil {
		if !errors.Is(err, errNoWritePermission) {
			return err
		}

		sudoRequired = true
	}

	// Copy a temp file in the binary folder.
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
		// Set temp permissions for the folder.
		if err = u.setPermissions("777", u.fDir, sudoRequired); err != nil {
			return err
		}

		defer func() {
			if errDefer := u.setPermissions(pathPerm, u.fDir, sudoRequired); errDefer != nil {
				u.Log().Error("error during setting folder permissions", "dir", u.fDir, "error", errDefer)
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

	// Rename a temp file to the original binary name.
	if err = os.Rename(fTmpName, u.fPath); err != nil {
		return err
	}

	// Set binary permissions.
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

// cleanup removes temporary data.
func (u *updateAction) cleanup() {
	if _, err := os.Stat(u.fTmpPath); err == nil {
		if err = os.Remove(u.fTmpPath); err != nil {
			u.Log().Error("error deleting file", "file", u.fTmpPath, "error", err)
		}
	}
}

// getOS return OS name and checks if it's supported.
func getOS() (os string, err error) {
	os = runtime.GOOS
	if os != "linux" && os != "darwin" {
		return os, errUnsupportedOS
	}
	os = strings.ToUpper(os[:1]) + os[1:]
	return os, nil
}

// getArch get OS arch.
func getArch() string {
	arch, ok := archMap[runtime.GOARCH]
	if !ok {
		arch = runtime.GOARCH // Fallback to the raw value if no mapping exists
	}

	return arch
}

func isCommandAvailable(name string) (bool, string) {
	var cmdPath string
	cmdPath, err := exec.LookPath(name)
	if err != nil {
		return false, ""
	}
	return true, cmdPath
}

func getUpdateCmd() (string, error) {
	sudoAvailable, _ := isCommandAvailable(sudoCmd)
	doasAvailable, _ := isCommandAvailable(doasCmd)
	chmodAvailable, _ := isCommandAvailable(chmodCmd)

	if !sudoAvailable && !doasAvailable {
		return "", fmt.Errorf("neither sudo or doas is available on your system. Please install one of them")
	}

	if !chmodAvailable {
		return "", fmt.Errorf("chmod is not available on your system. Please install it")
	}

	var cmd string
	if sudoAvailable {
		cmd = sudoCmd
	} else {
		cmd = doasCmd
	}

	return cmd, nil
}
