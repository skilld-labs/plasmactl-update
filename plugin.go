// Package plasmactlupdate implements an update launchr plugin
package plasmactlupdate

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/launchrctl/keyring"
	"github.com/launchrctl/launchr"
	"github.com/launchrctl/launchr/pkg/cli"
	"github.com/launchrctl/launchr/pkg/log"
	"github.com/spf13/cobra"
)

func init() {
	launchr.RegisterPlugin(&Plugin{})
}

// Plugin is launchr plugin providing update action.
type Plugin struct {
	k keyring.Keyring
}

// PluginInfo implements launchr.Plugin interface.
func (p *Plugin) PluginInfo() launchr.PluginInfo {
	return launchr.PluginInfo{
		Weight: 20,
	}
}

// OnAppInit implements launchr.Plugin interface.
func (p *Plugin) OnAppInit(app launchr.App) error {
	app.GetService(&p.k)
	return nil
}

// CobraAddCommands implements launchr.CobraPlugin interface to provide bump functionality.
func (p *Plugin) CobraAddCommands(rootCmd *cobra.Command) error {
	var ci keyring.CredentialsItem

	var updCmd = &cobra.Command{
		Use:   "update",
		Short: "Command to fetch and install latest version of plasmactl",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Don't show usage help on a runtime error.
			cmd.SilenceUsage = true
			u, err := createUpdateAction(p.k, ci)
			if err != nil {
				return err
			}

			return runUpdate(u)
		},
	}

	// Credentials flags
	ci.URL = baseURL
	updCmd.Flags().StringVarP(&ci.Username, "username", "u", "", "Username")
	updCmd.Flags().StringVarP(&ci.Password, "password", "p", "", "Password")
	rootCmd.AddCommand(updCmd)

	return nil
}

// runUpdate command entrypoint.
func runUpdate(u *updateAction) error {
	// Wrapper to conclude errors.
	if err := runCommands(u); err != nil {
		u.exitWithError()
		return err
	}

	return nil
}

// runCommands run commands one by one.
func runCommands(u *updateAction) error {
	cli.Println("Starting plasmactl installation...")

	currOS, arch, err := u.initVars()
	if err != nil {
		return err
	}

	// Check the validity of the credentials.
	if err = u.validateCredentials(); err != nil {
		return err
	}

	// Get value of Stable Release.
	sr, err := u.getStableRelease()
	if err != nil {
		return err
	}

	isUtd := isUpToDate(u.fName, sr)
	if isUtd {
		cli.Println("Current version of plasmactl is up to date.")
		return nil
	}

	// Format the URL with the determined 'os', 'arch' and 'extension' values.
	u.c.URL = fmt.Sprintf(binPathMask, baseURL, sr, currOS, arch, u.ext)
	cli.Println("Downloading file: %s", u.c.URL)

	// Download file to the temp folder.
	if err = u.downloadFile(); err != nil {
		return err
	}

	log.Debug("Binary path: %s\n", u.fPath)

	if err = u.installFile(u.fDir); err != nil {
		return err
	}

	// Outro.
	cli.Println("\u001B[0;32m%s has been installed successfully.\u001B[0m", u.fName)

	return nil
}

// isUpToDate check is current installed version of plasmactl is not up-to-date.
func isUpToDate(fName, sr string) bool {
	gV := strings.Split(strings.Replace(sr, "v", "", 1), ".")

	// Parse version command.
	cmd := exec.Command(fName, "--version")
	out, err := cmd.Output()
	if err != nil {
		return false
	}

	currVerOut := string(out)
	r := regexp.MustCompile(`plasmactl version\s+(v\d+\.\d+\.\d+)`)
	match := r.FindStringSubmatch(currVerOut)

	if len(match) > 1 && match[1] != "" {
		log.Debug("Installed version: %s", match[1])

		cV := strings.Split(strings.Replace(match[1], "v", "", 1), ".")

		if (gV[0] > cV[0]) || (gV[0] == cV[0] && gV[1] > cV[1]) || (gV[0] == cV[0] && gV[1] == cV[1] && gV[2] > cV[2]) {
			return false
		}
	} else {
		return false
	}

	return true
}
