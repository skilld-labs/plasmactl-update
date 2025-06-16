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
	"github.com/launchrctl/launchr/pkg/action"
)

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

	cfg         *config
	externalCfg string

	// runtime vars.
	c        keyring.CredentialsItem
	ext      string
	fName    string
	fTmpPath string
	fPath    string
	fDir     string
	sudoCmd  string
	os       string
	arch     string
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
func createUpdateAction(kr keyring.Keyring, cr keyring.CredentialsItem, externalCfg string) (*updateAction, error) {
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

	return &updateAction{k: kr, c: cr, sudoCmd: cmd, cfg: getUpdateConfig(), externalCfg: externalCfg}, nil
}

// Errors.
var (
	errUnsupportedOS    = errors.New("unsupported operating system")
	errUnsupportedArch  = errors.New("unsupported architecture")
	errInvalidCreds     = errors.New("failed to validate credentials")
	errMalformedKeyring = errors.New("the keyring is malformed or wrong passphrase provided")
)

// initVars initialize plugin variables.
func (u *updateAction) initVars() error {
	var err error

	// Get the operating system type.
	u.os, err = getOS()
	if err != nil {
		u.Term().Error().Printfln("Unsupported operating system: %s", u.os)
		return err
	}

	// Get the machine architecture.
	u.arch, err = getArch()
	if err != nil {
		if errors.Is(err, errUnsupportedArch) {
			u.Term().Printfln("Unsupported architecture: %s", u.arch)
		}

		return err
	}

	if u.externalCfg != "" {
		cfgExternal, err := parseConfigFromPath(u.externalCfg)
		if err != nil {
			return fmt.Errorf("error parsing external config file %s: %v", u.externalCfg, err)
		}
		u.cfg = cfgExternal
	}

	if u.cfg == nil {
		return fmt.Errorf("update config is not set, use --config flag or build launchr with predefined config")
	}

	err = validateConfig(u.cfg)
	if err != nil {
		u.Log().Debug("config validation failed", "error", err)
		return fmt.Errorf("not enough configuration for update. Please ensure your build is with correct tags. See debug for missing info")
	}

	// Set URL for credentials item.
	u.c.URL = u.cfg.RepositoryURL

	// Get username and password.
	if err = u.getCredentials(); err != nil {
		return err
	}

	// Prepare binary paths
	err = u.findExecPaths()
	if err != nil {
		return err
	}

	u.Log().Debug("initialized environment",
		"os", u.os, "arch", u.arch, "temp_path", u.fTmpPath, "url", u.c.URL,
		"base URL", u.cfg.RepositoryURL, "stable release", u.cfg.LatestStable, "bin_mask", u.cfg.BinMask,
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
	u.Log().Debug("get credentials for source url of release", "url", u.c.URL)

	// Get credentials and save in keyring.
	ci, err := u.k.GetForURL(u.c.URL)
	if err != nil {
		if errors.Is(err, keyring.ErrEmptyPass) {
			return err
		} else if !errors.Is(err, keyring.ErrNotFound) {
			return errMalformedKeyring
		}

		ci.URL = u.c.URL
		ci.Username = u.c.Username
		ci.Password = u.c.Password
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
		}
	}

	u.c = ci
	return err
}

// validateCredentials make request to validate credentials and return HTTP status code.
func (u *updateAction) validateCredentials() error {
	r, err := u.sendRequest(u.c.URL)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	switch r.StatusCode {
	case 0:
		u.Term().Error().Println("Failed to validate credentials. Access denied.")
		return errInvalidCreds
	case 200:
		u.Term().Success().Println("Valid credentials. Access granted.")
	case 401:
		u.Term().Error().Printfln("HTTP %d: Unauthorized. Credentials seems to be invalid.", r.StatusCode)
		return errInvalidCreds
	case 404:
		u.Term().Error().Printfln("HTTP %d: Not Found. File %s does not exist.", r.StatusCode, u.c.URL)
		return errInvalidCreds
	default:
		u.Term().Error().Printfln(
			"HTTP %d. An issue appeared while trying to validate credentials against %s.",
			r.StatusCode,
			u.c.URL,
		)
	}

	return nil
}

// sendRequest send HTTP request, make authorization and return response.
func (u *updateAction) sendRequest(url string) (*http.Response, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
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

// getStableRelease send request and get a stable release version.
func (u *updateAction) getStableRelease() (string, error) {
	stableReleaseURL := fmt.Sprintf("%s/%s", u.c.URL, u.cfg.LatestStable)
	resp, err := u.sendRequest(stableReleaseURL)

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
	url := fmt.Sprintf(u.cfg.BinMask, u.c.URL, version, u.os, u.arch, u.ext)
	u.Term().Printfln("Downloading file: %s", u.c.URL)

	resp, err := u.sendRequest(url)
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
			u.Log().Error("error deleting file", "file", u.fTmpPath, "error", err)
		}
	}

	u.Term().Error().Println("Update failed")
}

// getOS return OS name and checks if it's supported.
func getOS() (os string, err error) {
	os = runtime.GOOS
	if os != "linux" && os != "darwin" {
		return os, errUnsupportedOS
	}
	return os, nil
}

// getArch get OS arch.
func getArch() (arch string, err error) {
	arch = runtime.GOARCH
	if arch == "amd64" || arch == "386" || arch == "arm64" {
		return arch, nil
	}

	return arch, errUnsupportedArch
}
