// Package plasmactlupdate implements an update launchr plugin
package plasmactlupdate

import (
	"context"
	_ "embed"

	"github.com/launchrctl/keyring"
	"github.com/launchrctl/launchr"
	"github.com/launchrctl/launchr/pkg/action"
)

func init() {
	launchr.RegisterPlugin(&Plugin{})
}

//go:embed action.yaml
var actionYaml []byte

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

// DiscoverActions implements [launchr.ActionDiscoveryPlugin] interface.
func (p *Plugin) DiscoverActions(_ context.Context) ([]*action.Action, error) {
	a := action.NewFromYAML("update", actionYaml)
	a.SetRuntime(action.NewFnRuntime(func(_ context.Context, a *action.Action) error {
		input := a.Input()
		ci := keyring.CredentialsItem{
			URL:      "",
			Username: input.Opt("username").(string),
			Password: input.Opt("password").(string),
		}
		externalConfig := input.Opt("config").(string)

		log := launchr.Log()
		if rt, ok := a.Runtime().(action.RuntimeLoggerAware); ok {
			log = rt.LogWith()
		}

		term := launchr.Term()
		if rt, ok := a.Runtime().(action.RuntimeTermAware); ok {
			term = rt.Term()
		}

		u, err := createUpdateAction(p.k, ci, externalConfig)
		if err != nil {
			return err
		}
		u.SetLogger(log)
		u.SetTerm(term)

		return runUpdate(u)
	}))
	return []*action.Action{a}, nil
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
	u.Term().Info().Printfln("Starting %s installation...", version.Name)

	err := u.initVars()
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
		u.Term().Printfln("Current version of %s is up to date.", version.Name)
		return nil
	}

	// Download file to the temp folder.
	if err = u.downloadFile(stableRelease); err != nil {
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

// isUpToDate check is current installed version of plasmactl is not up-to-date.
func isUpToDate(stableRelease string) bool {
	version := launchr.Version()
	return version.Version == stableRelease
}
