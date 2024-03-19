package plasmactlupdate

import (
	"errors"
	"fmt"
	"github.com/launchrctl/keyring"
	"github.com/launchrctl/launchr/pkg/cli"
	"github.com/launchrctl/launchr/pkg/log"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Update stored update definition.
type Update struct {
	c        keyring.CredentialsItem
	ext      string
	fName    string
	fTmpPath string
	fPath    string
}

// CreateUpdate instance.
func CreateUpdate(cr keyring.CredentialsItem) (*Update, error) {
	return &Update{cr, "", "", "", ""}, nil
}

// Errors.
var (
	errUnsupportedOS   = errors.New("unsupported operating system")
	errUnsupportedArch = errors.New("unsupported architecture")
	errInvalidCreds    = errors.New("failed to validate credentials")
)

// Define the URL pattern for the file.
const (
	BaseUrl     = "https://repositories.skilld.cloud/repository/pla-plasmactl-raw"
	releasePath = "stable_release"
	binPathMask = "%s/%s/plasmactl_%s_%s%s"
)

// initVars initialize plugin variables.
func (u *Update) initVars() (string, string, error) {
	// Get username and password.
	if err := u.getCreds(); err != nil {
		return "", "", err
	}

	// Get the operating system type.
	currOS, err := u.getOS()
	if err != nil {
		return "", "", err
	}

	// Set Update vars.
	u.fName = fmt.Sprintf("plasmactl%s", u.ext)
	u.fTmpPath = filepath.Join(os.TempDir(), u.fName)

	// Get the machine architecture.
	arch, err := getArch()
	if err != nil {
		return "", "", err
	}

	u.c.URL = fmt.Sprintf("%s/%s", BaseUrl, releasePath)

	log.Debug("OS = %s\n", currOS)
	log.Debug("Arch = %s\n", arch)
	log.Debug("Temp file path: %s\n", u.fTmpPath)
	log.Debug("Source url of release: %s\n", u.c.URL)

	return currOS, arch, nil
}

// getCreds stores username and password credentials.
func (u *Update) getCreds() error {
	if u.c.Username != "" && u.c.Password != "" {
		return nil
	}

	if u.c.URL != "" {
		cli.Println("Enter credentials for %s", u.c.URL)
	}

	if err := keyring.RequestCredentialsFromTty(&u.c); err != nil {
		return err
	}

	return nil
}

// getOS check operating system supports and set extension package file.
func (u *Update) getOS() (os string, err error) {
	os = runtime.GOOS

	if os == "windows" {
		u.ext = ".exe"
	} else if os != "linux" && os != "darwin" {
		cli.Println("Unsupported operating system: %s", os)
		return "", errUnsupportedOS
	}
	return os, nil
}

// getArch get OS arch.
func getArch() (arch string, err error) {
	arch = runtime.GOARCH

	if arch == "amd64" || arch == "386" || arch == "arm64" {
		return arch, nil
	} else {
		cli.Println("Unsupported architecture: %s", arch)
		return "", errUnsupportedArch
	}
}

// validateCredentials make request to validate credentials and return HTTP status code.
func (u *Update) validateCredentials() error {
	r, err := u.sendRequest()
	if err != nil {
		return err
	}
	defer r.Body.Close()

	switch r.StatusCode {
	case 0:
		cli.Println("Error: Failed to validate credentials. Access denied.")
		return errInvalidCreds
	case 200:
		cli.Println("Valid credentials. Access granted.")
	case 401:
		cli.Println("Error: HTTP %d: Unauthorized. Credentials seems to be invalid.", r.StatusCode)
		return errInvalidCreds
	case 404:
		cli.Println("Error: HTTP %d: Not Found. File %s does not exist.", r.StatusCode, u.c.URL)
		return errInvalidCreds
	default:
		cli.Println(
			"Error: HTTP %d. An issue appeared while trying to validate credentials against %s.",
			r.StatusCode,
			u.c.URL,
		)
	}

	return nil
}

// sendRequest send HTTP request, make authorization and return response.
func (u *Update) sendRequest() (*http.Response, error) {
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
func (u *Update) getStableRelease() (string, error) {
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
	cli.Println("Stable release: %s", r)

	return r, nil

}

// downloadFile Download the file using with Basic Auth header.
func (u *Update) downloadFile() error {
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

// getBinPath get bin folder path.
func (u *Update) getBinPath(envPath, homeDir string) string {
	binPath := ""

	if strings.Contains(envPath, homeDir+"/.global/bin") {
		binPath = filepath.Join(homeDir, ".global/bin")
	} else if strings.Contains(envPath, homeDir+"/.local/bin") {
		binPath = filepath.Join(homeDir, ".local/bin")
	} else if strings.Contains(envPath, "/usr/local/bin") {
		binPath = "/usr/local/bin"
	}

	u.fPath = strings.TrimSpace(filepath.Join(binPath, u.fName))
	return binPath
}

// installFile copy file to the bin folder and remove temp file.
func (u *Update) installFile(dirPath string) error {
	cli.Println("Installing %s binary under %s", u.fName, dirPath)

	srcFile, err := os.Open(u.fTmpPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	destFile, err := os.Create(u.fPath)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err = io.Copy(destFile, srcFile); err != nil {
		return err
	}

	if err = os.Remove(u.fTmpPath); err != nil {
		return err
	}

	return nil
}

// exitWithError exit with error and remove temp file.
func (u *Update) exitWithError() {
	if _, err := os.Stat(u.fTmpPath); err == nil {
		if err = os.Remove(u.fTmpPath); err != nil {
			log.Err("Error file %s deleting: %s", u.fTmpPath, err)
		}
	}

	cli.Println("\033[31;31mUpdate failed\033[0m")
}
