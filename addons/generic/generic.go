package generic

import (
	"github.com/golang/glog"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/plugin"
)

func onPTPConfigChangeGeneric(*ptpv1.PtpProfile) error {
	glog.Infof("calling onPTPConfigChangeGeneric")
	return nil
}

func PopulateHwConfigGeneric(hwconfigs *[]ptpv1.HwConfig) error {
	return nil
}

func Reference(name string) *plugin.Plugin {
	if name != "reference" {
		glog.Errorf("Plugin must be initialized as 'reference'")
		return nil
	}
	return &plugin.Plugin{Name: "reference",
		OnPTPConfigChange: onPTPConfigChangeGeneric,
		PopulateHwConfig:  PopulateHwConfigGeneric,
	}
}
