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

		log := launchr.Log()
		if rt, ok := a.Runtime().(action.RuntimeLoggerAware); ok {
			log = rt.LogWith()
		}

		term := launchr.Term()
		if rt, ok := a.Runtime().(action.RuntimeTermAware); ok {
			term = rt.Term()
		}

		cmd, err := getUpdateCmd()
		if err != nil {
			return err
		}

		u := &updateAction{
			k:             p.k,
			credentials:   ci,
			sudoCmd:       cmd,
			cfg:           getUpdateConfig(),
			externalCfg:   input.Opt("config").(string),
			targetVersion: input.Opt("target").(string),
		}
		u.SetLogger(log)
		u.SetTerm(term)

		err = u.doRun()
		if err != nil {
			u.Term().Error().Println("Update failed")
			u.cleanup()
		}

		return err
	}))
	return []*action.Action{a}, nil
}
