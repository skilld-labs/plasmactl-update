// Package plasmactlupdate implements an update launchr plugin
package plasmactlupdate

import (
	"context"
	_ "embed"
	"fmt"

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

		var cfg *config
		// Check if the user submitted a custom config file.
		externalCfg := input.Opt("config").(string)
		if externalCfg != "" {
			cfgExternal, err := parseConfigFromPath(externalCfg)
			if err != nil {
				return fmt.Errorf("error parsing external config file %s: %v", externalCfg, err)
			}
			cfg = cfgExternal
		} else {
			cfg = getUpdateConfig()
			// Update the config with the user input if present.
			repoURL := input.Opt("repository-url").(string)
			if repoURL != "" {
				cfg.RepositoryURL = repoURL
			}
			pinnedRelease := input.Opt("release-file-mask").(string)
			if pinnedRelease != "" {
				cfg.PinnedRelease = pinnedRelease
			}
			binMask := input.Opt("bin-mask").(string)
			if binMask != "" {
				cfg.BinMask = binMask
			}
		}

		// Fallback to default config values if they are empty.
		if cfg.PinnedRelease == "" {
			cfg.PinnedRelease = defaultPinnedReleaseTpl
		}
		if cfg.BinMask == "" {
			cfg.BinMask = defaultBinTpl
		}

		u := &updateAction{
			k:             p.k,
			credentials:   ci,
			sudoCmd:       cmd,
			cfg:           cfg,
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
