// Package plasmactlupdate implements an update launchr plugin
package plasmactlupdate

import (
	"fmt"

	"github.com/launchrctl/keyring"
	"github.com/launchrctl/launchr"
)

func init() {
	launchr.RegisterPlugin(&Plugin{})
}

// Plugin is [launchr.Plugin] providing update action.
type Plugin struct {
	k keyring.Keyring
}

// PluginInfo implements [launchr.Plugin] interface.
func (p *Plugin) PluginInfo() launchr.PluginInfo {
	return launchr.PluginInfo{
		Weight: 20,
	}
}

// OnAppInit implements [launchr.OnAppInitPlugin] interface.
func (p *Plugin) OnAppInit(app launchr.App) error {
	app.GetService(&p.k)
	return nil
}

// CobraAddCommands implements [launchr.CobraPlugin] interface to provide update functionality.
func (p *Plugin) CobraAddCommands(rootCmd *launchr.Command) error {
	var ci keyring.CredentialsItem

	var updCmd = &launchr.Command{
		Use:   "update",
		Short: "Command to fetch and install latest version of " + rootCmd.Name(),
		RunE: func(cmd *launchr.Command, _ []string) error {
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
	version := launchr.Version()
	launchr.Term().Info().Printfln("Starting %s installation...", version.Name)

	currOS, arch, err := u.initVars()
	if err != nil {
		return err
	}

	// Check the validity of the credentials.
	if err = u.validateCredentials(); err != nil {
		return err
	}

	// Get value of Stable Release.
	stableRelease, err := u.getStableRelease()
	if err != nil {
		return err
	}

	if isUpToDate(stableRelease) {
		launchr.Term().Printfln("Current version of %s is up to date.", version.Name)
		return nil
	}

	// Format the URL with the determined 'os', 'arch' and 'extension' values.
	u.c.URL = fmt.Sprintf(binPathMask, baseURL, stableRelease, currOS, arch, u.ext)
	launchr.Term().Printfln("Downloading file: %s", u.c.URL)

	// Download file to the temp folder.
	if err = u.downloadFile(); err != nil {
		return err
	}

	launchr.Log().Debug("binary path", "path", u.fPath)

	if err = u.installFile(u.fDir); err != nil {
		return err
	}

	// Outro.
	launchr.Term().Success().Printfln("%s has been installed successfully.", u.fName)
	return nil
}

// isUpToDate check is current installed version of plasmactl is not up-to-date.
func isUpToDate(stableRelease string) bool {
	version := launchr.Version()
	return version.Version == stableRelease
}
