package daemon

import (
	"github.com/golang/glog"
	"github.com/k8snetworkplumbingwg/ptp-operator/addons"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/plugin"
)

type PluginManager struct {
	plugins map[string]*plugin.Plugin
}

func HelloWorld() {
	glog.Infof("hello world")
	return
}

func registerPlugins(plugins []string) PluginManager {
	glog.Infof("Begin plugin registration...")
	manager := PluginManager{plugins: make(map[string]*plugin.Plugin)}
	for _, name := range plugins {
		currentPlugin := registerPlugin(name)
		if currentPlugin != nil {
			manager.plugins[name] = currentPlugin
		}
	}
	return manager
}

func registerPlugin(name string) *plugin.Plugin {
	glog.Infof("Trying to register plugin: " + name)
	for mName, mConstructor := range mapping.PluginMapping {
		if mName == name {
			return mConstructor(name)
		}
	}
	glog.Errorf("Plugin not found: " + name)
	return nil
}

func (pm *PluginManager) OnPTPConfigChange(nodeProfile *ptpv1.PtpProfile) {
	for _, pluginObject := range pm.plugins {
		pluginObject.OnPTPConfigChange(nodeProfile)
	}
}

func (pm *PluginManager) PopulateHwConfig(hwconfigs *[]ptpv1.HwConfig) {
	for _, pluginObject := range pm.plugins {
		pluginObject.PopulateHwConfig(hwconfigs)
	}
}
